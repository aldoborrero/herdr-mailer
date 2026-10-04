{
  flake,
  inputs,
  pkgs,
  ...
}:
let
  mod = inputs.treefmt-nix.lib.evalModule pkgs {
    projectRootFile = "flake.nix";
    programs = {
      deadnix.enable = true;
      deadnix.no-lambda-pattern-names = true;
      deadnix.priority = 1;
      nixfmt.enable = true;
      nixfmt.priority = 2;
      gofmt.enable = true;
      shellcheck.enable = true;
      shellcheck.priority = 1;
      shellcheck.includes = [
        "*.sh"
        "*.envrc"
      ];
      shfmt.enable = true;
      shfmt.priority = 2;
      shfmt.includes = [
        "*.sh"
        "*.envrc"
      ];
      yamlfmt.enable = true;
      jsonfmt.enable = true;
      mdformat.enable = true;
      just.enable = true;
    };
    settings.excludes = [
      "AGENTS.md"
      "IMPLEMENTATION.md"
      ".go/**"
      ".agents/**"
      ".codex/**"
      "CLAUDE.md"
    ];
  };
  wrapper = mod.config.build.wrapper;
in
wrapper
// {
  passthru = wrapper.passthru // {
    tests.check = mod.config.build.check flake;
  };
}
