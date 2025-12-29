git add .
git commit -m "auto commit this version code"

git push origin main 

git remote rm github
git remote add github https://github.com/neko233-com/db233-go.git
git push github main