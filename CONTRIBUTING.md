# Contributing to Data Fabric

Issues and pull requests in English or Chinese are welcome.

## Before opening an issue

- Search existing issues and check the [documentation](README.md).
- Use the Bug report or Feature request form to provide reproducible details.
- Use [Discussions](https://github.com/RhinoLink-Tech/data-fabric/discussions) for questions and ideas.
- Report vulnerabilities privately as described in [SECURITY.md](SECURITY.md). Never post credentials, personal data, or exploitable details publicly.

## Development

Use the Go version declared in [go.mod](go.mod) and prepare the metadata database required by [the configuration](docs/configuration.md).

```bash
go mod download
go vet ./...
go test ./...
go build ./...
```

Format changed Go files with `gofmt`. CI checks formatting, runs `go vet` and tests, and builds all packages.

## Your first contribution

New here? Browse the [open `good first issue` list](https://github.com/RhinoLink-Tech/data-fabric/issues?q=is%3Aissue+is%3Aopen+label%3A%22good+first+issue%22) and pick a task that interests you.

1. Check that nobody is already on it: look at the issue's assignees and read the recent comments. Pick another issue if someone has already said they are working on it.
2. Comment on the issue that you would like to work on it and briefly say how you plan to approach it, so others can coordinate. You do not need to be assigned first, and there is no guaranteed response time, so if you hear nothing, please keep the scope small and start when you are ready.
3. Open a focused pull request and link the issue in its description (for example, `Fixes #123`).
4. Run the checks listed on the issue (and the checks under [Development](#development) for Go changes) and describe in the pull request what you ran.

## Pull requests

1. Fork the repository and create a focused branch.
2. Keep each change small and explain the user problem it solves.
3. Add or update tests for behavior changes; update docs when usage or configuration changes.
4. Include a changelog entry for user-visible changes.
5. Open a PR against `master`, link related issues, describe validation, and call out security or compatibility effects.
6. Keep required checks green and respond to review feedback.

See [docs/development.md](docs/development.md) and [docs/contributing.md](docs/contributing.md) for detailed guidance.

## 中文说明

欢迎使用中文提交 Issue 和 Pull Request。一般问题请使用 Discussions；Bug 和功能建议请使用对应模板；安全漏洞请按 [安全政策](SECURITY.md) 私下报告。开发环境使用 [go.mod](go.mod) 声明的 Go 版本，并按 [配置指南](docs/configuration.md) 准备元数据库。提交应聚焦、说明验证方式，并在行为变化时补充测试和文档。
