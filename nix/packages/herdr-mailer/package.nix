{
  lib,
  src,
  buildGoModule,
  nats-server,
  runtimes,
}:
buildGoModule {
  pname = "herdr-mailer";
  version = "0.1.0";
  inherit src;
  vendorHash = "sha256-y7LXiksnmJY/W8WH5CwfZqxXxedlQnlUTXMBZM5+hvA=";
  subPackages = [ "cmd/herdr-mailer" ];
  nativeCheckInputs = [ nats-server ];
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
