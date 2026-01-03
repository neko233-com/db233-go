
git push origin main
git push origin --tags

git remote rm github
git remote add github https://github.com/neko233-com/db233-go.git
git push github main
git push github --tags