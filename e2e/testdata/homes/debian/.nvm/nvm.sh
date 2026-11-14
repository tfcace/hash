# A stand-in for nvm-sh/nvm's nvm.sh, cut to the constructs that hung hash's
# startup: nvm() tests the shell's flags with ${-#*e}, ${-#*a} and ${-#*E}
# and, for one that is set, turns it off and calls itself again (nvm.sh,
# nvm()); sourcing the file ends with nvm_process_parameters, whose
# nvm_auto use runs `nvm use` on the default alias (nvm.sh, nvm_auto()).
# A shell that read every flag as set recursed forever.

nvm_echo() {
  command printf %s\\n "$*" 2>/dev/null
}

nvm_err() {
  >&2 nvm_echo "$@"
}

# nvm_version_dir prints the newest installed version matching $1 (22, v22.1).
nvm_version_dir() {
  local NVM_MATCH
  NVM_MATCH=''
  for d in "$NVM_DIR"/versions/node/v"${1#v}"*; do
    [ -d "$d" ] && NVM_MATCH="${d##*/}"
  done
  nvm_echo "$NVM_MATCH"
}

nvm_ls_current() {
  local NVM_LS_CURRENT_NODE_PATH
  if ! NVM_LS_CURRENT_NODE_PATH="$(command which node 2>/dev/null)"; then
    nvm_echo 'none'
  else
    case "$NVM_LS_CURRENT_NODE_PATH" in
      "$NVM_DIR"/*) nvm_echo "$(node --version 2>/dev/null)" ;;
      *) nvm_echo 'system' ;;
    esac
  fi
}

nvm() {
  if [ "$#" -lt 1 ]; then
    nvm_echo 'Usage: nvm use <version> | nvm current'
    return
  fi

  local DEFAULT_IFS
  DEFAULT_IFS=" $(nvm_echo t | command tr t \\t)
"
  if [ "${-#*e}" != "$-" ]; then
    set +e
    local EXIT_CODE
    local IFS
    IFS="${DEFAULT_IFS}"
    nvm "$@"
    EXIT_CODE="$?"
    set -e
    return "$EXIT_CODE"
  elif [ "${-#*a}" != "$-" ]; then
    set +a
    local EXIT_CODE
    local IFS
    IFS="${DEFAULT_IFS}"
    nvm "$@"
    EXIT_CODE="$?"
    set -a
    return "$EXIT_CODE"
  elif [ -n "${BASH-}" ] && [ "${-#*E}" != "$-" ]; then
    set +E
    local EXIT_CODE
    local IFS
    IFS="${DEFAULT_IFS}"
    nvm "$@"
    EXIT_CODE="$?"
    set -E
    return "$EXIT_CODE"
  elif [ "${IFS}" != "${DEFAULT_IFS}" ]; then
    local IFS
    IFS="${DEFAULT_IFS}"
    nvm "$@"
    return "$?"
  fi

  case "$1" in
    current)
      nvm_ls_current
      ;;
    use)
      shift
      [ "$1" = --silent ] && shift
      local VERSION
      VERSION="$(nvm_version_dir "$1")"
      if [ -z "$VERSION" ]; then
        nvm_err "N/A: version \"$1\" is not yet installed."
        return 3
      fi
      export NVM_BIN="$NVM_DIR/versions/node/$VERSION/bin"
      PATH="$NVM_BIN:$PATH"
      export PATH
      ;;
    *)
      nvm_err "nvm: unknown command $1"
      return 127
      ;;
  esac
}

nvm_auto() {
  local VERSION
  VERSION="$(command cat "$NVM_DIR/alias/default" 2>/dev/null)"
  if [ -n "$VERSION" ]; then
    nvm use --silent "$VERSION" >/dev/null
  fi
}

nvm_process_parameters() {
  nvm_auto use
}

nvm_process_parameters "$@"
