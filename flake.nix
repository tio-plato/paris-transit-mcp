{
  description = "Read-only MCP server for Île-de-France public transport, on PRIM data";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";

  outputs =
    { self, nixpkgs }:
    let
      systems = [
        "x86_64-linux"
        "aarch64-linux"
        "x86_64-darwin"
        "aarch64-darwin"
      ];
      forAllSystems = f: nixpkgs.lib.genAttrs systems (system: f nixpkgs.legacyPackages.${system});
    in
    {
      packages = forAllSystems (
        pkgs:
        let
          version = self.shortRev or self.dirtyShortRev or "dirty";
        in
        {
          default = pkgs.buildGoModule {
            pname = "paris-transit-mcp";
            inherit version;
            src = self;
            vendorHash = "sha256-+A01o/aeFALXmV2UxDIfLZG2W3vQ7E4e0kkHkp+ZFIY=";
            ldflags = [
              "-s"
              "-w"
              "-X main.version=${version}"
            ];
            meta = {
              description = "Read-only MCP server for Île-de-France public transport, on PRIM data";
              homepage = "https://github.com/tio-plato/paris-transit-mcp";
              mainProgram = "paris-transit-mcp";
            };
          };
        }
      );

      devShells = forAllSystems (pkgs: {
        default = pkgs.mkShell {
          packages = [
            pkgs.go
            pkgs.gopls
            pkgs.golangci-lint
            pkgs.goreleaser
          ];
        };
      });

      checks = forAllSystems (
        pkgs:
        let
          package = self.packages.${pkgs.stdenv.hostPlatform.system}.default;
        in
        {
          inherit package;
          # The linker ignores a -X for a symbol that doesn't exist, so ask the program itself.
          version = pkgs.runCommand "paris-transit-mcp-version" { } ''
            ${pkgs.bash}/bin/bash ${./scripts/smoke.sh} ${pkgs.lib.getExe package} ${package.version}
            touch $out
          '';
          # Reuses the package's vendored modules, so the lint runs offline in the sandbox.
          lint = package.overrideAttrs (old: {
            pname = "paris-transit-mcp-lint";
            nativeBuildInputs = old.nativeBuildInputs ++ [ pkgs.golangci-lint ];
            buildPhase = ''
              runHook preBuild
              export HOME=$TMPDIR GOLANGCI_LINT_CACHE=$TMPDIR/golangci-lint
              golangci-lint run ./...
              runHook postBuild
            '';
            doCheck = false;
            installPhase = ''
              runHook preInstall
              touch $out
              runHook postInstall
            '';
          });
        }
      );
    };
}
