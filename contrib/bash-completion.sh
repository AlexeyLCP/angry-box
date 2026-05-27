# bash completion for angry-box

_angry_box() {
    local cur prev words cword
    _init_completion || return

    local commands="deploy status config apply remove reload serve host chain apply-chain version"

    local host_sub="add list delete"
    local chain_sub="create list show delete"

    case "${prev}" in
        angry-box)
            COMPREPLY=($(compgen -W "${commands}" -- "${cur}"))
            return
            ;;
        host)
            COMPREPLY=($(compgen -W "${host_sub}" -- "${cur}"))
            return
            ;;
        chain)
            COMPREPLY=($(compgen -W "${chain_sub}" -- "${cur}"))
            return
            ;;
        --backend)
            COMPREPLY=($(compgen -W "sing-box xray" -- "${cur}"))
            return
            ;;
        --type)
            COMPREPLY=($(compgen -W "transport user" -- "${cur}"))
            return
            ;;
        --strategy)
            COMPREPLY=($(compgen -W "urltest failover selector bond" -- "${cur}"))
            return
            ;;
        --file|--key|--addr|--user|--listen)
            # Let readline do normal file completion for paths
            compopt -o filenames
            return
            ;;
    esac

    # Default: suggest commands or global flags
    COMPREPLY=($(compgen -W "${commands} --backend --file --config --addr --user --key --port --protocol --type --strategy" -- "${cur}"))
} &&
complete -F _angry_box angry-box
