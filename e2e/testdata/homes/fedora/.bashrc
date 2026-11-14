# .bashrc (Fedora's /etc/skel/.bashrc, then rbenv's and asdf's lines)

# Source global definitions. Fedora's reads /etc/bashrc; the fixture's copy
# of its constructs is in the home, so the test does not run the machine's.
if [ -f ~/etc/bashrc ]; then
	. ~/etc/bashrc
fi

# User specific environment
if ! [[ "$PATH" =~ "$HOME/.local/bin:$HOME/bin:" ]]
then
    PATH="$HOME/.local/bin:$HOME/bin:$PATH"
fi
export PATH

# Uncomment the following line if you don't like systemctl's auto-paging feature:
# export SYSTEMD_PAGER=

# User specific aliases and functions
if [ -d ~/.bashrc.d ]; then
	for rc in ~/.bashrc.d/*; do
		if [ -f "$rc" ]; then
			. "$rc"
		fi
	done
fi
unset rc

eval "$(rbenv init - bash 2>/dev/null)"
. "$HOME/.asdf/asdf.sh"
