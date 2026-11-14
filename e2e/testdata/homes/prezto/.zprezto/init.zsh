#
# A stand-in for sorin-ionescu/prezto's init.zsh (MIT License, Sorin
# Ionescu): its version check, then its module loop written with zsh's short
# for (`for zmodule ("$zmodules[@]") zmodload ...`, init.zsh line 183),
# which hash cannot load. Hash used to source it silently.
#

# Check for the minimum supported version.
min_zsh_version='4.3.11'
if ! autoload -Uz is-at-least || ! is-at-least "$min_zsh_version"; then
  printf "prezto: old shell detected, minimum required: %s\n" "$min_zsh_version" >&2
  return 1
fi
unset min_zsh_version

# Load Zsh modules.
zstyle -a ':prezto:load' zmodule 'zmodules'
for zmodule ("$zmodules[@]") zmodload "zsh/${(z)zmodule}"
unset zmodule{s,}

export PREZTO_LOADED=1
