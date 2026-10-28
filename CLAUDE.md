# MinXLSX

A portable, minimalist .xlsx editor in Go + Fyne + excelize.
Read docs/DESIGN.md before starting work; it is the source of truth for scope, architecture and milestones.

## Environment
- Developed in WSL Ubuntu; module path: github.com/siMoon646/minxlsx
- Targets: Windows 10/11 and desktop Linux, x86-64 only
- License: GPL-3.0 (public repo)

## Rules
- Never lose user data: every save must pass the round-trip tests.
- Only internal/model may import excelize; internal/ui and internal/app never touch files.
- Every edit goes through the undo stack.
- Saves are atomic: write a temp file in the same folder, then rename.
- Portable: never write outside the executable's folder (settings live in ./settings/).
- Never use Fyne’s Preferences API or app.NewWithID; portable settings live in ./settings/.
- Run `gofmt -l . && go vet ./... && golangci-lint run && go test ./...` before saying a task is done.
- One milestone per branch; small commits with clear messages.
- Ask before adding a new dependency.
- When a decision changes scope, tools, or milestones, update docs/DESIGN.md in the same branch.
- Use the Edit/Write tools for file changes, not shell scripts, so hooks and permissions apply.
