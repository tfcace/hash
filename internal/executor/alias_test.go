package executor

import (
	"context"
	"strings"
	"testing"
	"time"
)

// alias lists the aliases hash keeps as functions as bash's alias does:
// all of them, by name, as `alias NAME='value'`, or the ones it names, and
// `NAME: not found` with status 1 for a name that is no alias. A function
// of the name defined or unset since is no alias any more. unalias takes
// one out, its function with it.
func TestExecute_AliasListsTheAliases(t *testing.T) {
	tests := []struct{ line, want string }{
		{`alias ll='ls -l' gg="echo 'it'"; alias; echo "st=$?"`, "alias gg='echo '\\''it'\\'''\nalias ll='ls -l'\nst=0\n"},
		{`alias ll='ls -l'; alias ll; echo "st=$?"; alias -p`, "alias ll='ls -l'\nst=0\nalias ll='ls -l'\n"},
		{`alias nope 2>&1; echo "st=$?"`, "hash: alias: nope: not found\nst=1\n"},
		{`alias a='echo a' b='echo b'; b() { :; }; unset -f a; alias; echo done`, "done\n"},
		{`alias a='echo a'; unalias a; echo "st=$?"; alias; type a 2>/dev/null || echo gone`, "st=0\ngone\n"},
		{`unalias nope 2>&1; echo "st=$?"; alias a=x b=y; unalias -a; alias; echo done`, "hash: unalias: nope: not found\nst=1\ndone\n"},
		{`alias a='echo hi'; ( alias b='echo b' ); alias`, "alias a='echo hi'\n"},
	}
	for _, tc := range tests {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		var out, errb strings.Builder
		_, err := New().Execute(ctx, tc.line, &out, &errb)
		cancel()
		if err != nil || out.String() != tc.want {
			t.Errorf("%q: out %q, stderr %q, err %v; want %q", tc.line, out.String(), errb.String(), err, tc.want)
		}
	}
}

// An alias whose value calls the alias's own name (alias ls='ls -G')
// runs the command of that name, as bash does, which does not expand an
// alias again in its own text: its function no longer calls itself
// forever. The name escaped (\ls) or quoted is the same name.
func TestExecute_AliasCallsTheCommandOfItsOwnName(t *testing.T) {
	tests := []struct{ line, want string }{
		{`alias echo='echo X'; echo hi`, "X hi\n"},
		{`alias echo='\echo X'; echo hi`, "X hi\n"},
		{`alias echo="'echo' X"; echo hi`, "X hi\n"},
		{`alias echo='echo a; echo b'; echo hi`, "a\nb hi\n"},
		{`alias printf='LC_ALL=C printf'; printf '%s\n' hi`, "hi\n"},
		{`alias echo='echo X'; alias; type echo | head -1`, "alias echo='echo X'\necho is a function\n"},
	}
	for _, tc := range tests {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		var out, errb strings.Builder
		_, err := New().Execute(ctx, tc.line, &out, &errb)
		cancel()
		if err != nil || out.String() != tc.want {
			t.Errorf("%q: out %q, stderr %q, err %v; want %q", tc.line, out.String(), errb.String(), err, tc.want)
		}
	}
}

// An alias a subshell defines again leaves its shell's alias of the name
// as it was: each shell lists its own, as bash's alias does.
func TestExecute_AliasRedefinedInASubshell(t *testing.T) {
	tests := []struct{ line, want string }{
		{`alias a='echo 1'; (alias a='echo 2'; alias); alias; a`, "alias a='echo 2'\nalias a='echo 1'\n1\n"},
		{`alias a='echo 1'; (alias a='echo 2'); alias a='echo 3'; alias`, "alias a='echo 3'\n"},
	}
	for _, tc := range tests {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		var out, errb strings.Builder
		_, err := New().Execute(ctx, tc.line, &out, &errb)
		cancel()
		if err != nil || out.String() != tc.want {
			t.Errorf("%q: out %q, stderr %q, err %v; want %q", tc.line, out.String(), errb.String(), err, tc.want)
		}
	}
}

// Aliases that name each other end, as in bash, which expands no alias
// again inside its own expansion: the name met again runs the command of
// that name (alias ll='ls -d'; alias ls=ll makes ls run ls -d), or is not
// found when there is none. The alias works again once its call returned.
func TestExecute_AliasesThatNameEachOtherEnd(t *testing.T) {
	runLines(t, []struct{ line, want string }{
		{`alias ll='ls -d'; alias ls=ll; ls /; ls /`, "/\n/\n"},
		{`alias ll='ls -d'; alias ls=ll; ll / 2>/dev/null; echo "st=$?"`, "st=127\n"}, // ll is ls -d is ll -d: no command ll
		{`alias a1=b1; alias b1=c1; alias c1='echo end; a1'; a1 2>/dev/null; echo "st=$?"`, "end\nst=127\n"},
		{`alias ll='ls -d'; alias ls=ll; echo "$(ls /)" | cat; (ls /)`, "/\n/\n"},
		{`alias e2='echo two'; alias e1='e2; e2'; e1`, "two\ntwo\n"},
		{`alias ll='ls -d'; alias ls=ll; ls / >/dev/null; echo "[${__hash_expanding-}]"`, "[]\n"},
	})
}
