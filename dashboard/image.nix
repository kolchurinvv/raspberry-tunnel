# The dashboard's OCI image for the cluster (D3). It runs as a numeric
# non-root user, with the database and the tailnet state on a volume at
# /var/lib/dashboard. The user pushes it; agents don't.
{
  dockerTools,
  cacert,
  writeTextDir,
  dashboard,
}:

dockerTools.buildLayeredImage {
  name = "raspberry-tunnel-dashboard";
  tag = dashboard.version;
  architecture = "amd64";

  contents = [
    # For TLS to the control server.
    cacert
    (writeTextDir "etc/passwd" "nonroot:x:65532:65532:dashboard:/var/lib/dashboard:/sbin/nologin\n")
    (writeTextDir "etc/group" "nonroot:x:65532:\n")
  ];

  fakeRootCommands = ''
    mkdir -p var/lib/dashboard tmp
    chown 65532:65532 var/lib/dashboard
    chmod 0700 var/lib/dashboard
    chmod 1777 tmp
  '';

  config = {
    Entrypoint = [ "${dashboard}/bin/dashboard" ];
    User = "65532:65532";
    WorkingDir = "/var/lib/dashboard";
    Env = [
      "DASH_LISTEN=:8080"
      "DASH_STATE_DIR=/var/lib/dashboard"
      "HOME=/var/lib/dashboard"
      "SSL_CERT_FILE=${cacert}/etc/ssl/certs/ca-bundle.crt"
    ];
    ExposedPorts."8080/tcp" = { };
    Volumes."/var/lib/dashboard" = { };
  };
}
