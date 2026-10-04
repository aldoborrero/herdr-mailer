{
  lib,
  src,
  buildGoModule,
  nats-server,
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
