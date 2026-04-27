<!-- dfmt:v1 begin -->
# Context Discipline — REQUIRED

This project uses DFMT to keep large tool outputs from exhausting the
context window. **Read this section at the start of every conversation
in this project.**

## Rule 1 — Prefer DFMT tools over native tools

Always use DFMT's MCP tools when an output might exceed 2 KB:

| Native     | DFMT replacement |
|------------|------------------|
| `Bash`     | `dfmt_exec`      |
| `Read`     | `dfmt_read`      |
| `WebFetch` | `dfmt_fetch`     |
| `Glob`     | `dfmt_glob`      |
| `Grep`     | `dfmt_grep`      |
| `Edit`     | `dfmt_edit`      |
| `Write`    | `dfmt_write`     |

Include an `intent` argument on every call, describing what you need
from the output. The `intent` lets DFMT return the relevant portion of
a large output without flooding the context.

## Rule 2 — Record user decisions

When the user states a preference or correction ("use X instead of Y",
"do not modify Z"), call `dfmt_remember` with a `decision` tag so the
choice survives context compaction.

## Why these rules matter

Some agents do not provide hooks to enforce these rules automatically.
**Compliance is your responsibility as the agent.** A single raw shell
output above 8 KB can push earlier context out of the window, erasing
the conversation's history. Following the rules above preserves it.
<!-- dfmt:v1 end -->
