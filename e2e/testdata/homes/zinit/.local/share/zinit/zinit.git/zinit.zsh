#
# A stand-in for zdharma-continuum/zinit's zinit.zsh (MIT License): its first
# statements, with zsh's brace form of if
# (`if (( !${#ZINIT_TASKS} )) { ZINIT_TASKS=( "<no-data>" ); }`), which
# hash cannot load. Hash used to source it silently (it filtered to nothing).
#

typeset -gaH ZINIT_REGISTERED_PLUGINS ZINIT_TASKS ZINIT_RUN
typeset -ga zsh_loaded_plugins
if (( !${#ZINIT_TASKS} )) { ZINIT_TASKS=( "<no-data>" ); }

zinit() { :; }
export ZINIT_LOADED=1
