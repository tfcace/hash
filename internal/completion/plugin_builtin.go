package completion

// Built-in plugin specs use the exact same TOML format as user specs in
// <config>/completions/. They double as reference examples and can be
// overridden (or disabled) by a user spec that declares the same command.

const builtinDockerSpec = `
[plugin]
name = "docker"
description = "Containers, images, volumes, and networks for docker"
commands = ["docker"]
# Global flags that take a separate value, so "docker --context remote rm"
# still matches the rm rule instead of reading "remote" as the subcommand.
value_flags = [
  "-c", "--context", "--config", "-H", "--host",
  "-l", "--log-level", "--tlscacert", "--tlscert", "--tlskey",
]

# Running containers. "restart" is deliberately absent: it accepts stopped
# containers too, so it lives with the any-state rule below.
[[rules]]
subcommands = [
  "stop", "kill", "pause", "unpause", "attach", "top", "stats", "port", "update",
  "container stop", "container kill", "container pause",
  "container unpause", "container attach", "container top", "container stats",
  "container port", "container update",
]
[rules.source]
exec = ["docker", "ps", "--format", "{{.Names}}\t{{.ID}}  {{.Image}}  ({{.Status}})"]
delimiter = "\t"
value_column = 1
description_column = 2
timeout = "3s"
cache_ttl = "30s"

# Running containers where only the first positional is a container
# (what follows is a command to run inside it).
[[rules]]
subcommands = ["exec", "container exec"]
max_args = 1
[rules.source]
exec = ["docker", "ps", "--format", "{{.Names}}\t{{.ID}}  {{.Image}}  ({{.Status}})"]
delimiter = "\t"
value_column = 1
description_column = 2
timeout = "3s"
cache_ttl = "30s"

# All containers (any state).
[[rules]]
subcommands = [
  "rm", "inspect", "logs", "wait", "diff", "commit", "export", "rename", "cp", "restart",
  "container rm", "container inspect", "container logs", "container wait",
  "container diff", "container commit", "container export", "container rename", "container cp",
  "container restart",
]
[rules.source]
exec = ["docker", "ps", "-a", "--format", "{{.Names}}\t{{.ID}}  {{.Image}}  ({{.Status}})"]
delimiter = "\t"
value_column = 1
description_column = 2
timeout = "3s"
cache_ttl = "30s"

# Stopped containers.
[[rules]]
subcommands = ["start", "container start"]
[rules.source]
exec = [
  "docker", "ps", "-a",
  "--filter", "status=exited", "--filter", "status=created",
  "--format", "{{.Names}}\t{{.ID}}  {{.Image}}  ({{.Status}})",
]
delimiter = "\t"
value_column = 1
description_column = 2
timeout = "3s"
cache_ttl = "30s"

# Images where only the first positional is an image
# (docker run IMAGE cmd..., docker create IMAGE cmd...).
[[rules]]
subcommands = ["run", "create", "container run", "container create"]
max_args = 1
[rules.source]
exec = ["docker", "images", "--filter", "dangling=false", "--format", "{{.Repository}}:{{.Tag}}\t{{.ID}}  {{.Size}}"]
delimiter = "\t"
value_column = 1
description_column = 2
timeout = "3s"
cache_ttl = "30s"

# Images.
[[rules]]
subcommands = [
  "rmi", "push", "tag", "history", "save",
  "image rm", "image inspect", "image push", "image tag", "image history", "image save",
]
[rules.source]
exec = ["docker", "images", "--filter", "dangling=false", "--format", "{{.Repository}}:{{.Tag}}\t{{.ID}}  {{.Size}}"]
delimiter = "\t"
value_column = 1
description_column = 2
timeout = "3s"
cache_ttl = "30s"

# Volumes.
[[rules]]
subcommands = ["volume rm", "volume inspect"]
[rules.source]
exec = ["docker", "volume", "ls", "--format", "{{.Name}}\t{{.Driver}}"]
delimiter = "\t"
value_column = 1
description_column = 2
timeout = "3s"
cache_ttl = "30s"

# Networks.
[[rules]]
subcommands = ["network rm", "network inspect"]
[rules.source]
exec = ["docker", "network", "ls", "--format", "{{.Name}}\t{{.ID}}  {{.Driver}}"]
delimiter = "\t"
value_column = 1
description_column = 2
timeout = "3s"
cache_ttl = "30s"

# docker network connect|disconnect NETWORK CONTAINER: the first positional is
# a network, everything after it is a container. These two rules must stay in
# this order — the max_args = 1 rule stops matching once the network is given,
# so the container rule below picks up the rest.
[[rules]]
subcommands = ["network connect", "network disconnect"]
max_args = 1
[rules.source]
exec = ["docker", "network", "ls", "--format", "{{.Name}}\t{{.ID}}  {{.Driver}}"]
delimiter = "\t"
value_column = 1
description_column = 2
timeout = "3s"
cache_ttl = "30s"

[[rules]]
subcommands = ["network connect", "network disconnect"]
[rules.source]
exec = ["docker", "ps", "--format", "{{.Names}}\t{{.ID}}  {{.Image}}  ({{.Status}})"]
delimiter = "\t"
value_column = 1
description_column = 2
timeout = "3s"
cache_ttl = "30s"

# Contexts. "context show" is absent on purpose: it prints the current
# context and takes no arguments.
[[rules]]
subcommands = [
  "context use", "context rm", "context inspect", "context update",
  "context export",
]
[rules.source]
exec = ["docker", "context", "ls", "--format", "{{.Name}}\t{{.Description}}"]
delimiter = "\t"
value_column = 1
description_column = 2
timeout = "3s"
cache_ttl = "30s"

# Installed plugins.
[[rules]]
subcommands = [
  "plugin rm", "plugin enable", "plugin disable", "plugin inspect",
  "plugin push", "plugin set", "plugin upgrade",
]
[rules.source]
exec = ["docker", "plugin", "ls", "--format", "{{.Name}}\t{{.Description}}"]
delimiter = "\t"
value_column = 1
description_column = 2
timeout = "3s"
cache_ttl = "30s"

# Buildx builders.
[[rules]]
subcommands = ["builder use", "builder rm", "builder inspect", "builder stop"]
[rules.source]
exec = ["docker", "builder", "ls", "--format", "{{.Name}}"]
timeout = "3s"
cache_ttl = "30s"
`

func builtinPluginSpecs() []*PluginSpec {
	return []*PluginSpec{
		mustParsePluginSpec(builtinDockerSpec),
	}
}

func mustParsePluginSpec(data string) *PluginSpec {
	spec, err := ParsePluginSpec([]byte(data))
	if err != nil {
		panic("completion: invalid built-in plugin spec: " + err.Error())
	}
	return spec
}
