{
  lib,
  src,
  buildGoModule,
  runtimeShell,
  coreutils,
  gnugrep,
  nats-server,
  nodejs,
  runtimes,
}:
buildGoModule {
  pname = "herdr-mailer";
  version = "0.1.0";
  inherit src;
  vendorHash = "sha256-y7LXiksnmJY/W8WH5CwfZqxXxedlQnlUTXMBZM5+hvA=";
  subPackages = [ "cmd/herdr-mailer" ];
  nativeCheckInputs = [
    nats-server
    nodejs
  ];
  checkPhase = ''
    runHook preCheck
    go test ./...
    go test -race ./...
    go vet ./...
    runHook postCheck
  '';
  postInstall = ''
    ln -s ${runtimes}/bin/herdr-mailer-runtime $out/bin/herdr-mailer-runtime
    mkdir -p $out/share/herdr-mailer
    ln -s ${runtimes}/lib/herdr-mailer/runtimes $out/share/herdr-mailer/runtimes
    cp docs/native-runtimes.md $out/share/herdr-mailer/

    # Herdr plugin: link $out/share/herdr-mailer/plugin with `herdr plugin link`.
    plugin=$out/share/herdr-mailer/plugin
    cp -r plugin $plugin
    mkdir -p $plugin/bin
    ln -s ../../../../bin/herdr-mailer $plugin/bin/herdr-mailer
    for script in $plugin/scripts/*.sh; do
      substituteInPlace $script --replace-quiet "#!/usr/bin/env bash" "#!${runtimeShell}"
    done
    substituteInPlace $plugin/scripts/common.sh \
      --replace-fail @path@ ${
        lib.makeBinPath [
          coreutils
          gnugrep
        ]
      }
  '';
  doInstallCheck = true;
  installCheckPhase = ''
    runHook preInstallCheck
    export HOME=$(mktemp -d) HERDR_MAILER_STATE=$(mktemp -d)/state
    plugin=$out/share/herdr-mailer/plugin
    $plugin/scripts/start.sh 2>&1 | grep -q 'not initialized'
    $out/bin/herdr-mailer init >/dev/null
    $plugin/scripts/start.sh
    for _ in $(seq 50); do [ -S $HERDR_MAILER_STATE/daemon.sock ] && break; sleep 0.1; done
    [ -S $HERDR_MAILER_STATE/daemon.sock ]
    $plugin/scripts/start.sh | grep -q 'already running'
    $plugin/scripts/restart.sh
    $plugin/scripts/stop.sh
    $plugin/scripts/stop.sh | grep -q 'not running'
    runHook postInstallCheck
  '';
  meta = {
    description = "Durable local inter-agent messaging for Herdr";
    homepage = "https://github.com/aldoborrero/herdr-mailer";
    license = lib.licenses.mit;
    sourceProvenance = with lib.sourceTypes; [ fromSource ];
    maintainers = with lib.maintainers; [ aldoborrero ];
    mainProgram = "herdr-mailer";
    platforms = lib.platforms.unix;
  };
}
