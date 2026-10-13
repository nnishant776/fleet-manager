# Names the host and the guests share (design doc, N1), defined once: each
# guest's QEMU runs as a host user of its own, and the credentials it is handed
# live in a directory of its own, readable by that user's group only.
{
  userOf = name: "guest-${name}";
  credentialsDir = "/var/lib/guest-credentials";
}
