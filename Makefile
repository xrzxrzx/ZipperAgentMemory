# ZipperAgentMemory 构建入口（阶段 0 骨架版）
# 目标：build / test / run；Windows 下需安装 GNU Make（如 scoop install make）。
# Windows 上 go build -o 会自动补 .exe 后缀，bin/ 已由 .gitignore 忽略。

BINARY := bin/zipper-agent-memoryd

.PHONY: build test run

build:
	go build -o $(BINARY) ./cmd/zipper-agent-memoryd

test:
	go test ./...

run:
	go run ./cmd/zipper-agent-memoryd
