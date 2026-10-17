# Test of "forwarding denied unless allowed" on a host with containers, built
# from this flake's modules (tests/networking/forwarding-host). Two network
# namespaces stand for two networks the host is on, each joined to it by a
# veth pair: "client" (10.10.1.0/24) and "other" (10.10.2.0/24). With IPv4
# forwarding on, as NixOS sets it for Docker, the test checks that:
# - a container's published port is reachable from the client, through
#   address translation and the host's forwarding, for Docker and podman;
# - containers reach the client's network;
# - the two networks cannot reach each other through the host, which a
#   Docker host would otherwise allow;
# - a firewall reload leaves all of that as it is, Docker's and podman's own
#   tables included.
# Run it with `nix build .#checks.x86_64-linux.networking-forwarding`.
{ pkgs, inputs }:
let
  lib = pkgs.lib;
  mkHost = import ../../lib/mkHost.nix { inherit inputs; };
  hostNode = mkHost.modulesFor { hostName = "fwd"; hostDir = ./forwarding-host; };

  # A web server: busybox httpd serving one page.
  page = pkgs.writeTextDir "srv/index.html" "hello from a container\n";
  image = pkgs.dockerTools.buildImage {
    name = "web";
    tag = "test";
    copyToRoot = pkgs.buildEnv {
      name = "web-root";
      paths = [ pkgs.busybox page ];
      pathsToLink = [ "/bin" "/srv" ];
    };
    config.Cmd = [ "/bin/httpd" "-f" "-p" "80" "-h" "/srv" ];
  };

  # The two networks. Each namespace serves its own name on port 8000.
  networks = pkgs.writeShellApplication {
    name = "test-networks";
    runtimeInputs = with pkgs; [ iproute2 busybox ];
    text = ''
      for n in client:1 other:2; do
        name=''${n%:*}
        i=''${n#*:}
        ip netns add "$name"
        ip link add "v$name" type veth peer name eth0 netns "$name"
        ip addr add "10.10.$i.1/24" dev "v$name"
        ip link set "v$name" up
        ip -n "$name" link set lo up
        ip -n "$name" addr add "10.10.$i.2/24" dev eth0
        ip -n "$name" link set eth0 up
        ip -n "$name" route add default via "10.10.$i.1"
        mkdir -p "/srv/$name"
        echo "$name" > "/srv/$name/index.html"
        ip netns exec "$name" busybox httpd -p 8000 -h "/srv/$name"
      done
    '';
  };
in
pkgs.testers.runNixOSTest {
  name = "networking-forwarding";

  # As in tests/secrets/vm-test.nix: the host modules take flakeLib as a
  # module argument, and set nixpkgs.config themselves.
  node.specialArgs = hostNode.specialArgs;
  node.pkgsReadOnly = false;

  nodes.fwd = {
    imports = hostNode.modules ++ [{
      # No ESP and no bootable disk in the test VM.
      boot.loader.systemd-boot.enable = lib.mkForce false;
      boot.loader.grub.enable = lib.mkForce false;

      virtualisation.memorySize = 2048;
      environment.systemPackages = [ networks pkgs.curl ];
    }];
  };

  testScript = ''
    def reachable(ns, url, expect):
        fwd.wait_until_succeeds(
            f"ip netns exec {ns} curl -fsS --max-time 5 {url} | grep -q '{expect}'",
            timeout=60,
        )

    def unreachable(ns, url):
        fwd.fail(f"ip netns exec {ns} curl -fsS --max-time 5 {url}")

    def check():
        with subtest("published ports are reachable from another network"):
            reachable("client", "http://10.10.1.1:8080", "hello from a container")
            reachable("client", "http://10.10.1.1:8081", "hello from a container")

        with subtest("containers reach the network"):
            fwd.succeed("docker run --rm web:test wget -qO- http://10.10.1.2:8000 | grep -qx client")
            fwd.succeed("podman run --rm localhost/web:test wget -qO- http://10.10.1.2:8000 | grep -qx client")

        with subtest("the host does not route between its other networks"):
            unreachable("client", "http://10.10.2.2:8000")
            unreachable("other", "http://10.10.1.2:8000")

    fwd.start()
    fwd.wait_for_unit("multi-user.target")
    fwd.wait_for_unit("docker.service")

    with subtest("forwarding is on, and the firewall filters it"):
        fwd.succeed('test "$(sysctl -n net.ipv4.ip_forward)" = 1')
        fwd.succeed("nft list chain inet nixos-fw forward")

    fwd.succeed("test-networks")
    # Both networks reach the host itself.
    fwd.succeed("ip netns exec client ping -c1 -W2 10.10.1.1")
    fwd.succeed("ip netns exec other ping -c1 -W2 10.10.2.1")

    fwd.succeed("docker load < ${image}")
    # podman's image policy only takes images from registries and a few local
    # sources; Docker's daemon is one of them. The image is the only one.
    fwd.succeed("podman pull docker-daemon:web:test")
    fwd.succeed("podman tag $(podman images -q) localhost/web:test")
    fwd.succeed("docker run -d --name web -p 8080:80 web:test")
    fwd.succeed("podman run -d --name web -p 8081:80 localhost/web:test")

    check()

    with subtest("a firewall reload leaves Docker's and podman's tables alone"):
        fwd.succeed("systemctl reload nftables")
        check()
  '';
}
