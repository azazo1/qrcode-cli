[private]
default:
    @just --list

# just build
# 构建开发用二进制到 bin/qr, 版本号显示 dev-build.
build:
    go build -o bin/qr ./cmd/qr

# just run -h
# just run e "https://example.com"
# 直接运行 CLI, 参数原样传入.
run *args:
    go run ./cmd/qr {{args}}

# 根据当前平台生成发布产物, 产物在 dist/ 下.
dist:
    uv run --no-project python scripts/dist.py

# 运行全部测试.
test:
    go test ./...

# 检查代码格式与静态问题.
lint:
    gofmt -l ./cmd ./internal
    go vet ./...

# 交叉编译检查, 覆盖三个剪贴板实现平台.
check-platforms:
    GOOS=linux go build ./...
    GOOS=windows GOARCH=amd64 go build ./...
