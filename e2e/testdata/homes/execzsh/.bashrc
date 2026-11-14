# Users who cannot chsh to zsh switch to it from their .bashrc.
export RC=1
if [ -x "$HOME/bin/zsh" ]; then
  exec "$HOME/bin/zsh" -l
fi
export RC2=1
