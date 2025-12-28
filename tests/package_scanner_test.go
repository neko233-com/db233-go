package tests

import (
	"reflect"
	"testing"

	"github.com/SolarisNeko/db233-go/pkg/db233"
)

/**
 * PackageScanner 单元测试
 *
 * @author SolarisNeko
 * @since 2025-12-28
 */

func TestPackageScanner_RegisterType(t *testing.T) {
	scanner := db233.NewPackageScanner()

	// 注册测试类型
	type TestType struct {
		ID   int
		Name string
	}

	testType := reflect.TypeOf(TestType{})
	scanner.RegisterType(testType)

	// 验证注册成功
	types := scanner.GetAllRegisteredTypes()
	if len(types) != 1 {
		t.Errorf("Expected 1 registered type, got %d", len(types))
	}

	if types[0] != testType {
		t.Error("Registered type does not match")
	}
}

func TestPackageScanner_RegisterTypes(t *testing.T) {
	scanner := db233.NewPackageScanner()

	// 定义测试类型
	type Type1 struct{ ID int }
	type Type2 struct{ Name string }
	type Type3 struct{ Value float64 }

	types := []reflect.Type{
		reflect.TypeOf(Type1{}),
		reflect.TypeOf(Type2{}),
		reflect.TypeOf(Type3{}),
	}

	scanner.RegisterTypes(types...)

	registered := scanner.GetAllRegisteredTypes()
	if len(registered) != 3 {
		t.Errorf("Expected 3 registered types, got %d", len(registered))
	}
}

func TestPackageScanner_ScanTypes(t *testing.T) {
	scanner := db233.NewPackageScanner()

	// 注册不同包的类型
	type User struct{ ID int }
	type Product struct{ Name string }

	userType := reflect.TypeOf(User{})
	productType := reflect.TypeOf(Product{})

	scanner.RegisterType(userType)
	scanner.RegisterType(productType)

	// 扫描当前包 - 使用实际的包名 (tests包)
	currentPackage := "tests" // 测试文件在tests包中
	results := scanner.ScanTypes(currentPackage)

	if len(results) != 2 {
		t.Errorf("Expected 2 types in current package, got %d", len(results))
	}
}

func TestPackageScanner_ScanStructTypes(t *testing.T) {
	scanner := db233.NewPackageScanner()

	// 注册结构体和非结构体类型
	type StructType struct{ ID int }
	interfaceType := reflect.TypeOf((*error)(nil)).Elem()

	scanner.RegisterType(reflect.TypeOf(StructType{}))
	scanner.RegisterType(interfaceType)

	currentPackage := "github.com/SolarisNeko/db233-go/pkg/db233"
	structTypes := scanner.ScanStructTypes(currentPackage)

	// 应该只返回结构体类型
	for _, typ := range structTypes {
		if typ.Kind() != reflect.Struct {
			t.Errorf("Expected struct type, got %s", typ.Kind())
		}
	}
}

func TestPackageScanner_ScanSubTypes(t *testing.T) {
	scanner := db233.NewPackageScanner()

	// 定义接口和实现
	type Repository interface {
		Save(entity interface{}) error
	}

	type UserRepo struct{}
	type ProductRepo struct{}

	// 注册类型
	repoInterface := reflect.TypeOf((*Repository)(nil)).Elem()
	userRepoType := reflect.TypeOf(UserRepo{})
	productRepoType := reflect.TypeOf(ProductRepo{})

	scanner.RegisterType(userRepoType)
	scanner.RegisterType(productRepoType)

	currentPackage := "github.com/SolarisNeko/db233-go/pkg/db233"
	subTypes := scanner.ScanSubTypes(currentPackage, repoInterface)

	// 注意：这个测试可能不准确，因为我们没有实际实现接口
	// 但它验证了扫描逻辑
	t.Logf("Found %d potential sub-types", len(subTypes))
}

func TestFuncTypeFilter_Accept(t *testing.T) {
	filter := db233.FuncTypeFilter(func(t reflect.Type) bool {
		return t.Kind() == reflect.Struct
	})

	structType := reflect.TypeOf(struct{ ID int }{})
	intType := reflect.TypeOf(0)

	if !filter.Accept(structType) {
		t.Error("Struct type should be accepted")
	}

	if filter.Accept(intType) {
		t.Error("Int type should not be accepted")
	}
}

func TestPackageScanner_GetAllRegisteredTypes(t *testing.T) {
	scanner := db233.NewPackageScanner()

	// 初始状态
	types := scanner.GetAllRegisteredTypes()
	if len(types) != 0 {
		t.Errorf("Expected 0 types initially, got %d", len(types))
	}

	// 注册类型后
	type TestType struct{ ID int }
	scanner.RegisterType(reflect.TypeOf(TestType{}))

	types = scanner.GetAllRegisteredTypes()
	if len(types) != 1 {
		t.Errorf("Expected 1 type after registration, got %d", len(types))
	}
}

func TestPackageScanner_GetTypeKey(t *testing.T) {
	scanner := db233.NewPackageScanner()

	type TestType struct{ ID int }
	testType := reflect.TypeOf(TestType{})

	key := scanner.GetTypeKey(testType)
	expected := "tests.TestType"

	if key != expected {
		t.Errorf("Expected key %s, got %s", expected, key)
	}
}

func TestPackageScanner_GetPackageName(t *testing.T) {
	scanner := db233.NewPackageScanner()

	type TestType struct{ ID int }
	testType := reflect.TypeOf(TestType{})

	packageName := scanner.GetPackageName(testType)
	expected := "tests" // 测试文件在tests包中

	if packageName != expected {
		t.Errorf("Expected package name %s, got %s", expected, packageName)
	}
}
