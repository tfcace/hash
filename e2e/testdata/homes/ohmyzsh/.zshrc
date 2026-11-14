# If you come from bash you might have to change your $PATH.
# export PATH=$HOME/bin:$HOME/.local/bin:/usr/local/bin:$PATH

# Path to your Oh My Zsh installation.
export ZSH="$HOME/.oh-my-zsh"

ZSH_THEME="robbyrussell"
zstyle ':omz:update' mode reminder  # just remind me to update when it's time
plugins=(git docker z)

source $ZSH/oh-my-zsh.sh

# User configuration
export LANG=en_US.UTF-8
export EDITOR='nvim'
alias zshconfig="mate ~/.zshrc"

export PYENV_ROOT="$HOME/.pyenv"
[[ -d $PYENV_ROOT/bin ]] && export PATH="$PYENV_ROOT/bin:$PATH"
eval "$(pyenv init - zsh 2>/dev/null)"

autoload -U +X bashcompinit && bashcompinit
complete -o nospace -C /opt/homebrew/bin/terraform terraform
setopt HIST_IGNORE_ALL_DUPS
bindkey -e
export ZSHRC_RAN=1
