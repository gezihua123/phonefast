#!/bin/bash
# 用法: ./rename-module.sh

OLD_MODULE="github.com/phonefast/phonefast"
NEW_MODULE="github.com/gezihua123/phonefast"

echo "正在修改 go.mod..."
sed -i '' "s|$OLD_MODULE|$NEW_MODULE|g" go.mod

echo "正在修改所有 .go 文件中的导入路径..."
find . -name "*.go" -type f -exec sed -i '' "s|$OLD_MODULE|$NEW_MODULE|g" {} +

echo "正在整理依赖..."
go mod tidy

echo "✅ 修改完成！"