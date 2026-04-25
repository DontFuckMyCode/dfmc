#compdef dfmc

_dfmc() {
    local -a commands
    commands=(
        'help:Show help'
        'version:Show version'
        'doctor:Run diagnostics'
        'status:Show engine status'
        'chat:Interactive chat'
        'ask:Single-turn ask'
        'review:Review code'
        'analyze:Analyze code'
        'explain:Explain code'
        'init:Initialize project'
        'config:Manage configuration'
        'scan:Scan workspace'
        'magicdoc:Manage magic doc'
        'drive:Drive mode'
        'remote:Remote control'
        'mcp:MCP server'
        'serve:Serve web API'
        'completion:Generate completions'
    )

    _describe 'command' commands
}

dfmc() {
    _dfmc "$@"
}

_dfmc "$@"