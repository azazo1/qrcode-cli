# 版本号取自 git describe; 不在 git 仓库里时退回 dev.
version := `git describe --tags --always --dirty 2>/dev/null || echo dev`
ldflags := "-X github.com/azazo1/qrcode-cli/internal/cli.Version=" + version

[private]
default:
    @just --list

# just build
# 构建 CLI 二进制到 bin/qr.
build:
    go build -ldflags "{{ldflags}}" -o bin/qr ./cmd/qr

# just run -h
# just run e "https://example.com"
# 直接运行 CLI, 参数原样传入.
run *args:
    go run ./cmd/qr {{args}}

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
