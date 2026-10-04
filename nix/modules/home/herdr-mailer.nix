{
  config,
  lib,
  pkgs,
  ...
}:
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
    codexPackage = lib.mkOption {
      type = lib.types.nullOr lib.types.package;
      default = null;
      description = "Optional installed Codex package with queue support";
    };
    stateDirectory = lib.mkOption {
      type = lib.types.str;
      default = "${config.xdg.stateHome}/herdr-mailer";
    };
  };
  config = lib.mkIf cfg.enable {
    home.packages = [ cfg.package ];
    systemd.user.services.herdr-mailer = {
      Unit.Description = "Durable local Herdr mailer";
      Service = {
        ExecStart = "${lib.getExe cfg.package} --state ${lib.escapeShellArg cfg.stateDirectory} daemon";
        Environment =
          lib.optional (cfg.codexPackage != null)
            "PATH=${lib.makeBinPath [ cfg.codexPackage ]}";
        UMask = "0077";
        Restart = "on-failure";
        RestartSec = 3;
      };
      Install.WantedBy = [ "default.target" ];
    };
  };
}
