package db233

import (
	"bytes"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"math"
	"math/bits"
	"reflect"
	"strconv"
	"time"
)

var (
	strictSQLScannerType = reflect.TypeOf((*sql.Scanner)(nil)).Elem()
	strictTimeType       = reflect.TypeOf(time.Time{})
)

type strictConversionOptions struct {
	mayScan     bool
	retainBytes bool
}

// convertValueStrict converts one driver value without silent numeric wrapping
// or truncation. It is intentionally separate from convertValue so legacy ORM
// conversion semantics stay compatible.
func (o *OrmHandler) convertValueStrict(source any, targetType reflect.Type) (reflect.Value, error) {
	return o.convertValueStrictWithOptions(source, targetType, strictConversionOptionsFor(targetType))
}

func (o *OrmHandler) convertValueStrictWithOptions(
	source any,
	targetType reflect.Type,
	options strictConversionOptions,
) (reflect.Value, error) {
	return o.convertValueStrictInternal(source, targetType, options, false)
}

func (o *OrmHandler) convertValueStrictInternal(
	source any,
	targetType reflect.Type,
	options strictConversionOptions,
	sourceOwned bool,
) (reflect.Value, error) {
	if targetType == nil {
		return reflect.Value{}, fmt.Errorf("严格转换目标类型不能为 nil")
	}

	if rawBytes, ok := source.([]byte); ok && options.retainBytes && !sourceOwned {
		source = bytes.Clone(rawBytes)
		sourceOwned = true
	}

	// A nil SQL value keeps pointer fields nil. Value Scanner fields still receive
	// nil so sql.Null* and custom Scanner implementations can update validity.
	if source == nil && targetType.Kind() == reflect.Ptr {
		return reflect.Zero(targetType), nil
	}
	if scanned, handled, err := strictScanWithTarget(source, targetType, options.mayScan); handled {
		if err != nil {
			return reflect.Value{}, err
		}
		return scanned, nil
	}
	if source == nil {
		return reflect.Zero(targetType), nil
	}

	if targetType.Kind() == reflect.Ptr {
		elem, err := o.convertValueStrictInternal(source, targetType.Elem(), options, sourceOwned)
		if err != nil {
			return reflect.Value{}, err
		}
		ptr := reflect.New(targetType.Elem())
		ptr.Elem().Set(elem)
		return ptr, nil
	}

	sourceValue := reflect.ValueOf(source)
	if sourceValue.Type().AssignableTo(targetType) {
		return sourceValue, nil
	}

	switch targetType.Kind() {
	case reflect.Interface:
		if sourceValue.Type().AssignableTo(targetType) {
			return sourceValue, nil
		}
		return reflect.Value{}, fmt.Errorf("无法将 %T 赋值给 %s", source, targetType)

	case reflect.String:
		text, err := strictDriverValueText(source)
		if err != nil {
			return reflect.Value{}, err
		}
		result := reflect.New(targetType).Elem()
		result.SetString(text)
		return result, nil

	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if value, ok, err := strictSignedInteger(sourceValue, source, targetType); ok {
			return value, err
		}
		text, err := strictDriverValueText(source)
		if err != nil {
			return reflect.Value{}, err
		}
		value, err := strconv.ParseInt(text, 10, targetType.Bits())
		if err != nil {
			return reflect.Value{}, fmt.Errorf("无法将 %T(%q) 无损转换为 %s: %w", source, text, targetType, err)
		}
		result := reflect.New(targetType).Elem()
		result.SetInt(value)
		return result, nil

	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		if value, ok, err := strictUnsignedInteger(sourceValue, source, targetType); ok {
			return value, err
		}
		text, err := strictDriverValueText(source)
		if err != nil {
			return reflect.Value{}, err
		}
		value, err := strconv.ParseUint(text, 10, targetType.Bits())
		if err != nil {
			return reflect.Value{}, fmt.Errorf("无法将 %T(%q) 无损转换为 %s: %w", source, text, targetType, err)
		}
		result := reflect.New(targetType).Elem()
		result.SetUint(value)
		return result, nil

	case reflect.Float32, reflect.Float64:
		if value, ok, err := strictFloat(sourceValue, source, targetType); ok {
			return value, err
		}
		text, err := strictDriverValueText(source)
		if err != nil {
			return reflect.Value{}, err
		}
		value, err := strconv.ParseFloat(text, targetType.Bits())
		if err != nil {
			return reflect.Value{}, fmt.Errorf("无法将 %T(%q) 转换为 %s: %w", source, text, targetType, err)
		}
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return reflect.Value{}, fmt.Errorf("无法将 %T(%q) 转换为有限 %s", source, text, targetType)
		}
		result := reflect.New(targetType).Elem()
		result.SetFloat(value)
		return result, nil

	case reflect.Bool:
		value, err := driver.Bool.ConvertValue(source)
		if err != nil {
			return reflect.Value{}, fmt.Errorf("无法将 %T 转换为 %s: %w", source, targetType, err)
		}
		result := reflect.New(targetType).Elem()
		result.SetBool(value.(bool))
		return result, nil

	case reflect.Struct:
		if sourceValue.Type().ConvertibleTo(targetType) && sourceValue.Kind() == targetType.Kind() {
			return sourceValue.Convert(targetType), nil
		}
		if strictTimeType.ConvertibleTo(targetType) {
			text, err := strictDriverValueText(source)
			if err != nil {
				return reflect.Value{}, err
			}
			parsed, err := o.parseTime(text)
			if err != nil {
				return reflect.Value{}, fmt.Errorf("转换为 %s 失败: %w", targetType, err)
			}
			return reflect.ValueOf(parsed).Convert(targetType), nil
		}
		data, err := strictJSONSource(source)
		if err != nil {
			return reflect.Value{}, err
		}
		return o.unmarshalJSONValue(data, targetType)

	case reflect.Slice:
		if targetType.Elem().Kind() == reflect.Uint8 {
			if sourceValue.Kind() == reflect.Slice && sourceValue.Type().Elem().Kind() == reflect.Uint8 {
				return sourceValue.Convert(targetType), nil
			}
			text, err := strictDriverValueText(source)
			if err != nil {
				return reflect.Value{}, err
			}
			return reflect.ValueOf([]byte(text)).Convert(targetType), nil
		}
		data, err := strictJSONSource(source)
		if err != nil {
			return reflect.Value{}, err
		}
		return o.unmarshalJSONValue(data, targetType)

	case reflect.Map, reflect.Array:
		data, err := strictJSONSource(source)
		if err != nil {
			return reflect.Value{}, err
		}
		return o.unmarshalJSONValue(data, targetType)
	}

	if sourceValue.Kind() == targetType.Kind() && sourceValue.Type().ConvertibleTo(targetType) {
		return sourceValue.Convert(targetType), nil
	}
	return reflect.Value{}, fmt.Errorf("无法严格转换类型: %s -> %s", sourceValue.Type(), targetType)
}

func strictSignedInteger(sourceValue reflect.Value, source any, targetType reflect.Type) (reflect.Value, bool, error) {
	switch sourceValue.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		value := sourceValue.Int()
		bits := targetType.Bits()
		if bits < 64 {
			minValue := -(int64(1) << (bits - 1))
			maxValue := (int64(1) << (bits - 1)) - 1
			if value < minValue || value > maxValue {
				return reflect.Value{}, true, fmt.Errorf("%T(%d) 溢出 %s", source, value, targetType)
			}
		}
		return reflect.ValueOf(value).Convert(targetType), true, nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		value := sourceValue.Uint()
		bits := targetType.Bits()
		maxValue := ^uint64(0) >> 1
		if bits < 64 {
			maxValue = (uint64(1) << (bits - 1)) - 1
		}
		if value > maxValue {
			return reflect.Value{}, true, fmt.Errorf("%T(%d) 溢出 %s", source, value, targetType)
		}
		return reflect.ValueOf(int64(value)).Convert(targetType), true, nil
	case reflect.Float32, reflect.Float64:
		text := strconv.FormatFloat(sourceValue.Float(), 'f', -1, sourceValue.Type().Bits())
		value, err := strconv.ParseInt(text, 10, targetType.Bits())
		if err != nil {
			return reflect.Value{}, true, fmt.Errorf("无法将 %T(%q) 无损转换为 %s: %w", source, text, targetType, err)
		}
		return reflect.ValueOf(value).Convert(targetType), true, nil
	default:
		return reflect.Value{}, false, nil
	}
}

func strictUnsignedInteger(sourceValue reflect.Value, source any, targetType reflect.Type) (reflect.Value, bool, error) {
	bits := targetType.Bits()
	maxValue := ^uint64(0)
	if bits < 64 {
		maxValue = (uint64(1) << bits) - 1
	}
	switch sourceValue.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		value := sourceValue.Int()
		if value < 0 || uint64(value) > maxValue {
			return reflect.Value{}, true, fmt.Errorf("%T(%d) 溢出 %s", source, value, targetType)
		}
		return reflect.ValueOf(uint64(value)).Convert(targetType), true, nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		value := sourceValue.Uint()
		if value > maxValue {
			return reflect.Value{}, true, fmt.Errorf("%T(%d) 溢出 %s", source, value, targetType)
		}
		return reflect.ValueOf(value).Convert(targetType), true, nil
	case reflect.Float32, reflect.Float64:
		text := strconv.FormatFloat(sourceValue.Float(), 'f', -1, sourceValue.Type().Bits())
		value, err := strconv.ParseUint(text, 10, targetType.Bits())
		if err != nil {
			return reflect.Value{}, true, fmt.Errorf("无法将 %T(%q) 无损转换为 %s: %w", source, text, targetType, err)
		}
		return reflect.ValueOf(value).Convert(targetType), true, nil
	default:
		return reflect.Value{}, false, nil
	}
}

func strictFloat(sourceValue reflect.Value, source any, targetType reflect.Type) (reflect.Value, bool, error) {
	var value float64
	switch sourceValue.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		integer := sourceValue.Int()
		precision := 53
		if targetType.Bits() == 32 {
			precision = 24
		}
		magnitude := uint64(integer)
		if integer < 0 {
			magnitude = uint64(-(integer + 1)) + 1
		}
		if !strictIntegerExactlyRepresentable(magnitude, precision) {
			return reflect.Value{}, true, fmt.Errorf("%T(%d) 无法无损表示为 %s", source, integer, targetType)
		}
		value = float64(integer)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		integer := sourceValue.Uint()
		precision := 53
		if targetType.Bits() == 32 {
			precision = 24
		}
		if !strictIntegerExactlyRepresentable(integer, precision) {
			return reflect.Value{}, true, fmt.Errorf("%T(%d) 无法无损表示为 %s", source, integer, targetType)
		}
		value = float64(integer)
	case reflect.Float32, reflect.Float64:
		value = sourceValue.Float()
		if sourceValue.Kind() == reflect.Float64 && targetType.Bits() == 32 &&
			!math.IsNaN(value) && float64(float32(value)) != value {
			return reflect.Value{}, true, fmt.Errorf("%T(%v) 无法无损表示为 %s", source, source, targetType)
		}
	default:
		return reflect.Value{}, false, nil
	}

	if targetType.Bits() == 32 && !math.IsInf(value, 0) &&
		(value > math.MaxFloat32 || value < -math.MaxFloat32) {
		return reflect.Value{}, true, fmt.Errorf("%T(%v) 溢出 %s", source, source, targetType)
	}
	return reflect.ValueOf(value).Convert(targetType), true, nil
}

func strictIntegerExactlyRepresentable(value uint64, precision int) bool {
	if value == 0 {
		return true
	}
	width := bits.Len64(value)
	return width <= precision || bits.TrailingZeros64(value) >= width-precision
}

func strictScanWithTarget(source any, targetType reflect.Type, mayScan bool) (reflect.Value, bool, error) {
	if !mayScan || !strictTargetImplementsScanner(targetType) {
		return reflect.Value{}, false, nil
	}

	if targetType.Kind() == reflect.Ptr && targetType.Implements(strictSQLScannerType) {
		target := reflect.New(targetType.Elem())
		scanner := target.Interface().(sql.Scanner)
		if err := scanner.Scan(source); err != nil {
			return reflect.Value{}, true, fmt.Errorf("sql.Scanner 转换到 %s 失败: %w", targetType, err)
		}
		return target, true, nil
	}

	target := reflect.New(targetType)
	scanner, ok := target.Interface().(sql.Scanner)
	if !ok {
		return reflect.Value{}, false, nil
	}
	if err := scanner.Scan(source); err != nil {
		return reflect.Value{}, true, fmt.Errorf("sql.Scanner 转换到 %s 失败: %w", targetType, err)
	}
	return target.Elem(), true, nil
}

func strictTargetImplementsScanner(targetType reflect.Type) bool {
	if targetType == nil || targetType.Kind() == reflect.Interface {
		return false
	}
	if targetType.Kind() == reflect.Ptr && targetType.Implements(strictSQLScannerType) {
		return true
	}
	return reflect.PointerTo(targetType).Implements(strictSQLScannerType)
}

func strictConversionOptionsFor(targetType reflect.Type) strictConversionOptions {
	var options strictConversionOptions
	if targetType == nil {
		return options
	}

	current := targetType
	for {
		if strictTargetImplementsScanner(current) {
			options.mayScan = true
			options.retainBytes = true
		}
		if current.Kind() != reflect.Ptr {
			break
		}
		current = current.Elem()
	}
	if current.Kind() == reflect.Interface ||
		(current.Kind() == reflect.Slice && current.Elem().Kind() == reflect.Uint8) {
		options.retainBytes = true
	}
	return options
}

func strictDriverValueText(source any) (string, error) {
	if timestamp, ok := source.(time.Time); ok {
		return timestamp.Format(time.RFC3339Nano), nil
	}

	value := reflect.ValueOf(source)
	if !value.IsValid() {
		return "", fmt.Errorf("不能将 NULL 转换为文本")
	}
	switch value.Kind() {
	case reflect.String:
		return value.String(), nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(value.Int(), 10), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return strconv.FormatUint(value.Uint(), 10), nil
	case reflect.Float32, reflect.Float64:
		return strconv.FormatFloat(value.Float(), 'g', -1, value.Type().Bits()), nil
	case reflect.Bool:
		return strconv.FormatBool(value.Bool()), nil
	case reflect.Slice:
		if value.Type().Elem().Kind() == reflect.Uint8 {
			return string(value.Bytes()), nil
		}
	}
	return "", fmt.Errorf("无法将 %T 转换为文本", source)
}

func strictJSONSource(source any) ([]byte, error) {
	value := reflect.ValueOf(source)
	if !value.IsValid() {
		return nil, fmt.Errorf("不能将 NULL 转换为 JSON")
	}
	switch value.Kind() {
	case reflect.String:
		return []byte(value.String()), nil
	case reflect.Slice:
		if value.Type().Elem().Kind() == reflect.Uint8 {
			return value.Bytes(), nil
		}
	}
	return nil, fmt.Errorf("无法将 %T 转换为 JSON 目标", source)
}
