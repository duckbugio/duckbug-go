# AGENTS.md

Канонические инструкции для ВСЕХ кодинг-агентов: Claude Code (через симлинк `CLAUDE.md → AGENTS.md`), Codex, Cursor, Gemini CLI и любых будущих.

duckbug-go — официальный Go SDK DuckBug (модуль `github.com/duckbugio/duckbug-go`): отправка ошибок и логов в ingest DuckBug.

- Тесты: `go test ./...` (стандартный тулчейн, Taskfile здесь нет).
- Контракт протокола — репозиторий `duckbug-sdk-spec`; изменения формата событий сверяй с ним и с серверным ingest (`duckbug`), а поведение — с другими SDK (`duckbug-js`, `duckbug-php`, `duckbug-js-react`).
- Отвечай на русском; код, коммиты и документация — на английском.
- **Без AI-соавторства**: никаких `Co-Authored-By: <ИИ>` в коммитах (для Claude Code продублировано в `.claude/settings.json`).
- **Не коммитить и не пушить без явной команды.**
