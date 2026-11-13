{
  description = "raspberry-tunnel";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";

  # The SSH public key that root on a Pi accepts. The repository only holds a
  # placeholder, which Pi builds refuse, so each build is given the right Pi's
  # key from outside it: --override-input admin-key path:/absolute/path/to/key.pub
  inputs.admin-key = {
    url = "path:./keys/admin.pub";
    flake = false;
  };

  # Networks that tunnel traffic may never reach, on top of the private ranges in
  # modules/local-nets.nix. They're specific to a site, so the repository only
  # holds a placeholder, which Pi builds refuse. Each build is given a Nix list:
  # --override-input extra-local-nets path:/absolute/path/to/extra-local-nets.nix
  inputs.extra-local-nets = {
    url = "path:./modules/extra-local-nets.placeholder.nix";
    flake = false;
  };

  outputs = { self, nixpkgs, admin-key, extra-local-nets }:
    let
      systems = [ "x86_64-linux" "aarch64-linux" "x86_64-darwin" "aarch64-darwin" ];
      forAllSystems = f: nixpkgs.lib.genAttrs systems (system: f nixpkgs.legacyPackages.${system});
      mkPi = host: nixpkgs.lib.nixosSystem {
        specialArgs.adminKey = admin-key.outPath;
        specialArgs.extraLocalNetsFile = extra-local-nets.outPath;
        modules = [ ./modules/pi-hardware.nix ./modules/common.nix host ];
      };
    in
    {
      nixosConfigurations = {
        pi-a = mkPi ./hosts/pi-a.nix;
        pi-b = mkPi ./hosts/pi-b.nix;
      };

      checks.x86_64-linux.tunnel = nixpkgs.legacyPackages.x86_64-linux.testers.runNixOSTest ./tests/tunnel.nix;

      # The public dashboard (D3), for the cluster, whose nodes are all
      # x86_64: its binary, its image, and a check that runs its Go tests.
      packages.x86_64-linux.dashboard = nixpkgs.legacyPackages.x86_64-linux.callPackage ./dashboard/package.nix { };
      packages.x86_64-linux.dashboard-image = nixpkgs.legacyPackages.x86_64-linux.callPackage ./dashboard/image.nix {
        inherit (self.packages.x86_64-linux) dashboard;
      };
      checks.x86_64-linux.dashboard = self.packages.x86_64-linux.dashboard;

      devShells = forAllSystems (pkgs: {
        default = pkgs.mkShell {
          packages = [ 
              # Runs the justfile's recipes.
              pkgs.just
          ];
        };
      });
    };
}
