{
  description = "Hygge — a terminal AI coding assistant";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs =
    {
      self,
      nixpkgs,
      flake-utils,
    }:
    flake-utils.lib.eachDefaultSystem (
      system:
      let
        pkgs = nixpkgs.legacyPackages.${system};

        # go.mod declares "go 1.26.3"; pin the toolchain explicitly so the
        # build does not drift with the nixpkgs default.
        go = pkgs.go_1_26;

        hygge = pkgs.buildGoModule {
          pname = "hygge";
          version = "0.13.3";

          src = ./.;

          inherit go;

          # Replace with the hash printed by `nix build` on first run.
          vendorHash = pkgs.lib.fakeHash;

          subPackages = [ "cmd/hygge" ];

          ldflags = [
            "-s"
            "-w"
          ];

          meta = with pkgs.lib; {
            description = "Hygge — a terminal AI coding assistant";
            homepage = "https://github.com/cfbender/hygge";
            license = licenses.mit;
            mainProgram = "hygge";
          };
        };
      in
      {
        packages.default = hygge;
        packages.hygge = hygge;
      }
    );
}
