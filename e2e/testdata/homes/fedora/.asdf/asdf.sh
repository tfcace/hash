# A stand-in for asdf's asdf.sh: its PATH lines and the asdf function.
export ASDF_DIR="${ASDF_DIR:-$HOME/.asdf}"
_asdf_bin="$ASDF_DIR/bin"
_asdf_shims="${ASDF_DATA_DIR:-$HOME/.asdf}/shims"
case ":$PATH:" in *":${_asdf_bin}:"*) ;; *) PATH="${_asdf_bin}:$PATH" ;; esac
case ":$PATH:" in *":${_asdf_shims}:"*) ;; *) PATH="${_asdf_shims}:$PATH" ;; esac
export PATH
unset _asdf_bin _asdf_shims
asdf() { echo "asdf $*"; }
