# GitHub's SSH host keys, for every fetch a fleet machine makes over SSH: its
# configuration (modules/linux/fleet/config-fetch.nix), its secrets
# (lib/secrets-sync.nix) and the install's clone (lib/apps/os-install.nix).
# Without them a machine trusts whatever key it first sees for github.com, so
# a machine in the middle at that moment, at an install say, would be trusted
# from then on. A forge other than GitHub is still learned on first use.
#
# The keys are GitHub's published ones: the ssh_keys of
# https://api.github.com/meta, as of 2026-10-21. If GitHub ever changes them,
# fetches from it stop, as they do today on a host that has learned the old
# key, until this file is updated.
let
  # As a known_hosts file has them; also published as lib.forgeHostKeys.text,
  # for fleet host enroll to write on a machine not on the fleet yet.
  text = ''
    github.com ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl
    github.com ecdsa-sha2-nistp256 AAAAE2VjZHNhLXNoYTItbmlzdHAyNTYAAAAIbmlzdHAyNTYAAABBBEmKSENjQEezOmxkZMy7opKgwFB9nkt5YRrYMjNuG5N87uRgg6CLrbo5wAdT/y6v0mKV0U2w0WZ2YB/++Tpockg=
    github.com ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABgQCj7ndNxQowgcQnjshcLrqPEiiphnt+VTTvDP6mHBL9j1aNUkY4Ue1gvwnGLVlOhGeYrnZaMgRK6+PKCUXaDbC7qtbW8gIkhL7aGCsOr/C56SJMy/BCZfxd1nWzAOxSDPgVsmerOBYfNqltV9/hWCqBywINIR+5dIg6JTJ72pcEpEjcYgXkE2YEFXV1JHnsKgbLWNlhScqb2UmyRkQyytRLtL+38TGxkxCflmO+5Z8CSSNY7GidjMIZ7Q4zMjA2n1nGrlTDkzwDCsw+wqFPGQA179cnfGWOWRVruj16z6XyvxvjJwbz0wQZ75XK5tKSb7FNyeIEs4TT4jk+S4dhPeAUC5y+bDYirYgM4GC7uEnztnZyaVWQ7B381AK4Qdrwt51ZqExKbQpTUNn+EjqoTwvqNj4kqx5QUCI0ThS/YkOxJCXmPUWZbhjpCg56i+2aB6CmK2JGhn57K5mj0MNdBXA4/WnwH6XoPWJzK5Nyu2zB3nAZp+S5hpQs+p1vN1/wsjk=
  '';
  knownHosts = builtins.toFile "github-known-hosts" text;
  algorithms = "ssh-ed25519,ecdsa-sha2-nistp256,rsa-sha2-512,rsa-sha2-256";
in
{
  inherit text algorithms;
  # Options for ssh, before UserKnownHostsFile and StrictHostKeyChecking=
  # accept-new. ssh checks a host against both files. accept-new takes a key
  # of a type the files have no entry of for that host as new, and the server
  # chooses what it offers, so only the types GitHub has keys of are allowed:
  # one in the middle cannot offer another.
  options = "-o GlobalKnownHostsFile=${knownHosts} -o HostKeyAlgorithms=${algorithms}";
}
