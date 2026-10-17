# systemd sandbox for network-facing services in the VMs, on top of what
# DynamicUser already implies (ProtectSystem=strict, ProtectHome=read-only,
# PrivateTmp, NoNewPrivileges). Go programs raise their file limit with
# setrlimit at start, so @resources stays allowed.
{
  # Ranges a unit is kept out of: the LANs, link-local, CGNAT and the ULA.
  privateRanges = [
    "10.0.0.0/8"
    "172.16.0.0/12"
    "192.168.0.0/16"
    "169.254.0.0/16"
    "100.64.0.0/10"
    "fc00::/7"
    "fe80::/10"
  ];

  serviceConfig = {
    DynamicUser = true;
    CapabilityBoundingSet = "";
    AmbientCapabilities = "";
    PrivateDevices = true;
    PrivateUsers = true;
    ProtectClock = true;
    ProtectControlGroups = true;
    ProtectHostname = true;
    ProtectKernelLogs = true;
    ProtectKernelModules = true;
    ProtectKernelTunables = true;
    ProtectProc = "invisible";
    ProcSubset = "pid";
    LockPersonality = true;
    MemoryDenyWriteExecute = true;
    RestrictNamespaces = true;
    RestrictRealtime = true;
    RestrictAddressFamilies = [ "AF_INET" "AF_INET6" "AF_UNIX" "AF_NETLINK" ];
    SystemCallArchitectures = "native";
    SystemCallFilter = [ "@system-service" "~@privileged" ];
    UMask = "0077";
  };
}
