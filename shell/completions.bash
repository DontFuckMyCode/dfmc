#!/usr/bin/env bash
# dfmc bash completion

__dfmc_debug() {
    [[ -n "${BASH_COMP_DEBUG_FILE:-}" ]] && printf "%s\n" "$*" >&2
}

_dfmc() {
    local cur prev cword
    _init_completion -n : || return

    case $cword in
        1)
            COMPREPLY=($(compgen -W "help version doctor status chat ask review analyze explain init config scan magicdoc drive remote mcp serve completion" -- "$cur"))
            ;;
        2)
            case $prev in
                drive)
                    COMPREPLY=($(compgen -W "start stop resume status list active" -- "$cur"))
                    ;;
                serve)
                    COMPREPLY=($(compgen -W "--host --port --auth --token" -- "$cur"))
                    ;;
                mcp)
                    COMPREPLY=($(compgen -W "start stop status" -- "$cur"))
                    ;;
                remote)
                    COMPREPLY=($(compgen -W "start stop status list" -- "$cur"))
                    ;;
                completion)
                    COMPREPLY=($(compgen -W "bash zsh fish powershell" -- "$cur"))
                    ;;
                analyze|magicdoc)
                    COMPREPLY=($(compgen -W "--security --complexity --dead-code --full" -- "$cur"))
                    ;;
                config)
                    COMPREPLY=($(compgen -W "sync-models show set get" -- "$cur"))
                    ;;
                init)
                    COMPREPLY=($(compgen -W "--dir --profile --no-env" -- "$cur"))
                    ;;
                *)
                    ;;
            esac
            ;;
        *)
            case ${words[1]} in
                serve)
                    case $prev in
                        --host|--port|--auth|--token)
                            return
                            ;;
                    esac
                    ;;
            esac
            ;;
    esac

    # path completions for positional args that look like paths
    if [[ "$cur" == /* || "$cur" == .* ]]; then
        _filedir
    fi
}

__dfmc_wrap_gcc() {
    printf 'gcc -O2 -fPIC -I/usr/include -I%s 2>/dev/null\n' "$(pkg-config --cflags glib-2.0 2>/dev/null || echo "")"
}

complete -F _dfmc dfmc

# also let dfmc generate completions via `dfmc completion bash`
# this is a no-op fallback so the shell doesn't complain if dfmc isn't installed