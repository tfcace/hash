package completion

// Built-in plugin specs use the exact same TOML format as user specs in
// <config>/completions/. They double as reference examples and can be
// overridden (or disabled) by a user spec that declares the same command.

const builtinDockerSpec = `
[plugin]
name = "docker"
description = "Containers, images, volumes, and networks for docker"
commands = ["docker"]

# Running containers.
[[rules]]
subcommands = [
  "stop", "kill", "restart", "pause", "unpause", "attach", "top", "stats", "port", "update",
  "container stop", "container kill", "container restart", "container pause",
  "container unpause", "container attach", "container top", "container stats",
  "container port", "container update",
]
[rules.source]
exec = ["docker", "ps", "--format", "{{.Names}}\t{{.ID}}  {{.Image}}  ({{.Status}})"]
delimiter = "\t"
value_column = 1
description_column = 2

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

# All containers (any state).
[[rules]]
subcommands = [
  "rm", "inspect", "logs", "wait", "diff", "commit", "export", "rename", "cp",
  "container rm", "container inspect", "container logs", "container wait",
  "container diff", "container commit", "container export", "container rename", "container cp",
]
[rules.source]
exec = ["docker", "ps", "-a", "--format", "{{.Names}}\t{{.ID}}  {{.Image}}  ({{.Status}})"]
delimiter = "\t"
value_column = 1
description_column = 2

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

# Volumes.
[[rules]]
subcommands = ["volume rm", "volume inspect"]
[rules.source]
exec = ["docker", "volume", "ls", "--format", "{{.Name}}\t{{.Driver}}"]
delimiter = "\t"
value_column = 1
description_column = 2

# Networks.
[[rules]]
subcommands = ["network rm", "network inspect", "network connect", "network disconnect"]
[rules.source]
exec = ["docker", "network", "ls", "--format", "{{.Name}}\t{{.ID}}  {{.Driver}}"]
delimiter = "\t"
value_column = 1
description_column = 2
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
