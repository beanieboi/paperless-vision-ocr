# Patched mac-ocr fork (Apple Vision OCR and searchable PDFs). The source is
# the pinned flake input; `version` must match what that commit reports.
{
  lib,
  swiftPackages,
  fetchFromGitHub,
  src,
}:
let
  # SwiftPM can't fetch inside the sandbox; point the package at a local copy.
  # Keep in step with the fork's Package.resolved.
  argumentParser = fetchFromGitHub {
    owner = "apple";
    repo = "swift-argument-parser";
    tag = "1.7.1";
    hash = "sha256-90ECc3iEmxvOUk9iLKbQdQEz88dOisPqWsJLOFcKUV8=";
  };
in
swiftPackages.stdenv.mkDerivation (finalAttrs: {
  pname = "mac-ocr";
  version = "1.1.1-paperless.3";
  inherit src;

  nativeBuildInputs = with swiftPackages; [
    swift
    swiftpm
  ];

  # SwiftPM names a path package after its directory, hence the copy.
  postPatch = ''
    cp -r ${argumentParser} swift-argument-parser
    chmod -R u+w swift-argument-parser
    substituteInPlace Package.swift --replace-fail \
      '.package(url: "https://github.com/apple/swift-argument-parser.git", from: "1.5.0")' \
      '.package(path: "swift-argument-parser")'
    rm Package.resolved
  '';

  installPhase = ''
    runHook preInstall
    install -Dm755 "$(swiftpmBinPath)/mac-ocr" $out/bin/mac-ocr
    runHook postInstall
  '';

  # Fails the build if the pinned commit and `version` disagree.
  doInstallCheck = true;
  installCheckPhase = ''
    runHook preInstallCheck
    reported="$($out/bin/mac-ocr --version)"
    if [ "$reported" != "${finalAttrs.version}" ]; then
      echo "mac-ocr reports $reported, expected ${finalAttrs.version}" >&2
      exit 1
    fi
    $out/bin/mac-ocr searchable-pdf --help | grep -q -- --transcript-output
    runHook postInstallCheck
  '';

  meta = {
    description = "Apple Vision OCR CLI (paperless-vision-ocr fork)";
    homepage = "https://github.com/beanieboi/mac-ocr";
    license = lib.licenses.mit;
    mainProgram = "mac-ocr";
    platforms = lib.platforms.darwin;
  };
})
