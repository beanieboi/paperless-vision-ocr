{
  description = "Azure Document Intelligence-compatible remote OCR for Paperless-ngx, backed by Apple Vision";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";

    # Fork with the searchable-PDF fixes and --transcript-output, see
    # docs/mac-ocr-patch.md. Pinned to a commit on purpose: the adapter
    # refuses to start without these features, so only move it together with
    # a tested change here. scripts/install-mac-ocr.sh reads this line.
    mac-ocr = {
      url = "github:beanieboi/mac-ocr/243c8efb4dfc8061e5b9e368ae328810c8cc7872";
      flake = false;
    };
  };

  outputs =
    { self, nixpkgs, ... }@inputs:
    let
      forAllSystems =
        f:
        nixpkgs.lib.genAttrs [ "aarch64-darwin" "x86_64-darwin" ] (
          system: f nixpkgs.legacyPackages.${system}
        );
    in
    {
      packages = forAllSystems (pkgs: rec {
        mac-ocr = pkgs.callPackage ./nix/mac-ocr.nix { src = inputs.mac-ocr; };
        paperless-vision-ocr = pkgs.callPackage ./nix/package.nix {
          inherit mac-ocr;
          version = self.shortRev or self.dirtyShortRev or "dev";
        };
        default = paperless-vision-ocr;
      });

      darwinModules.default = import ./nix/darwin-module.nix self;

      checks = forAllSystems (pkgs: {
        inherit (self.packages.${pkgs.stdenv.hostPlatform.system}) mac-ocr paperless-vision-ocr;
      });

      formatter = forAllSystems (pkgs: pkgs.nixfmt);
    };
}
