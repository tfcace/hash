#
# Executes commands at the start of an interactive session.
# (sorin-ionescu/prezto's runcoms/zshrc)
#
# Authors:
#   Sorin Ionescu <sorin.ionescu@gmail.com>
#

# Source Prezto.
if [[ -s "${ZDOTDIR:-$HOME}/.zprezto/init.zsh" ]]; then
  source "${ZDOTDIR:-$HOME}/.zprezto/init.zsh"
fi

# Customize to your needs...
alias pz="echo prezto-alias"; export PREZRC_RAN=1
