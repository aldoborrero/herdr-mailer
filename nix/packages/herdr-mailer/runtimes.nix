{
  lib,
  buildNpmPackage,
  nodejs,
  src,
}:
buildNpmPackage {
  pname = "herdr-mailer-runtimes";
  version = "0.1.0";
  inherit src;
  sourceRoot = "source/runtimes";
  npmDepsFetcherVersion = 2;
  npmDepsHash = "sha256-FXMHvDQRfIWR42ilSqrp5xh9Hz3/XocCCKIaTR0vDXw=";
  npmFlags = [ "--ignore-scripts" ];
  dontNpmBuild = true;
  doCheck = true;
  checkPhase = ''
    runHook preCheck
    npm test
    runHook postCheck
  '';
  installPhase = ''
    mkdir -p $out/lib/herdr-mailer/runtimes $out/bin
    cp *.js package.json $out/lib/herdr-mailer/runtimes/
    npm prune --omit=dev --ignore-scripts
    cp -r node_modules $out/lib/herdr-mailer/runtimes/
    cat > $out/bin/herdr-mailer-runtime <<SCRIPT
    #!${nodejs}/bin/node
    import '$out/lib/herdr-mailer/runtimes/launch.js';
    SCRIPT
    chmod +x $out/bin/herdr-mailer-runtime
  '';
  meta = {
    description = "Native Claude Channels and Pi extension bridges for herdr-mailer";
    license = lib.licenses.mit;
    platforms = lib.platforms.unix;
  };
}
