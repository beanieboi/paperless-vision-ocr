{
  lib,
  buildGoModule,
  go_1_27,
  mac-ocr,
  version,
}:
(buildGoModule.override { go = go_1_27; }) {
  pname = "paperless-vision-ocr";
  inherit version;

  src = lib.fileset.toSource {
    root = ../.;
    fileset = lib.fileset.unions [
      ../go.mod
      ../cmd
      ../internal
      ../testdata
    ];
  };

  # No dependencies outside the standard library.
  vendorHash = null;

  subPackages = [ "cmd/paperless-vision-ocr" ];
  ldflags = [
    "-s"
    "-w"
    "-X main.version=${version}"
    # MAC_OCR_PATH still overrides this.
    "-X github.com/beanieboi/paperless-vision-ocr/internal/config.DefaultMacOCRPath=${lib.getExe mac-ocr}"
  ];

  # subPackages would limit `go test` to ./cmd. The Vision integration test
  # skips here: mac-ocr is not on PATH in the build.
  checkPhase = ''
    runHook preCheck
    go test ./...
    runHook postCheck
  '';

  passthru = { inherit mac-ocr; };

  meta = {
    description = "Azure Document Intelligence-compatible remote OCR for Paperless-ngx, backed by Apple Vision";
    homepage = "https://github.com/beanieboi/paperless-vision-ocr";
    license = lib.licenses.mit;
    mainProgram = "paperless-vision-ocr";
    platforms = lib.platforms.darwin;
  };
}
