{ config, lib, ... }:
let
  cfg = config.services.herdr-mailer;
in
{
  options.services.herdr-mailer = {
    enable = lib.mkEnableOption "private Herdr mailer daemon";
    package = lib.mkOption {
      type = lib.types.package;
      description = "herdr-mailer package from this flake";
    };
    user = lib.mkOption {
      type = lib.types.str;
      description = "Existing runtime owner; initialize their private state before starting";
    };
    stateDirectory = lib.mkOption {
      type = lib.types.str;
      description = "Absolute private state directory owned by the runtime user";
    };
    codexPackage = lib.mkOption {
      type = lib.types.nullOr lib.types.package;
      default = null;
      description = "Optional Codex package with native queue support";
    };
  };
  config = lib.mkIf cfg.enable {
    systemd.services.herdr-mailer = {
      description = "Durable local Herdr mailer";
      wantedBy = [ "multi-user.target" ];
      path = lib.optional (cfg.codexPackage != null) cfg.codexPackage;
      serviceConfig = {
        User = cfg.user;
        ExecStart = "${lib.getExe cfg.package} --state ${lib.escapeShellArg cfg.stateDirectory} daemon";
        UMask = "0077";
        Restart = "on-failure";
        RestartSec = 3;
      };
    };
  };
}
