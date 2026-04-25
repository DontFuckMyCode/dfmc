# fish completion for dfmc

function __dfmc_command
    set -l cmd $argv[1]
    switch $cmd
        case help version doctor status chat ask review analyze explain init config scan magicdoc drive remote mcp serve completion
            return 0
        case drive
            if test (count $argv) -ge 2
                set -l sub $argv[2]
                switch $sub
                    case start stop resume status list active
                        return 0
                end
            end
            return 1
        case serve
            if test (count $argv) -ge 2
                set -l sub $argv[2]
                switch $sub
                    --host --port --auth --token
                        return 0
                end
            end
            return 1
    end
    return 1
end

complete -c dfmc -f -a 'help version doctor status chat ask review analyze explain init config scan magicdoc drive remote mcp serve completion'

complete -c dfmc -n '__fish_seen_subcommand_from drive' -f -a 'start stop resume status list active'
complete -c dfmc -n '__fish_seen_subcommand_from serve' -f -a '--host --port --auth --token'
complete -c dfmc -n '__fish_seen_subcommand_from mcp' -f -a 'start stop status'
complete -c dfmc -n '__fish_seen_subcommand_from remote' -f -a 'start stop status list'
complete -c dfmc -n '__fish_seen_subcommand_from completion' -f -a 'bash zsh fish powershell'
complete -c dfmc -n '__fish_seen_subcommand_from analyze magicdoc' -f -a '--security --complexity --dead-code --full'
complete -c dfmc -n '__fish_seen_subcommand_from config' -f -a 'sync-models show set get'
complete -c dfmc -n '__fish_seen_subcommand_from init' -f -a '--dir --profile --no-env'