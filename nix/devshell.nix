{ pkgs, perSystem }:
perSystem.devshell.mkShell {
  packages = with pkgs; [
    just
    perSystem.self.formatter
    # Go development, vet and language server.
    go
    gopls
    gotools
    # Mandatory private JetStream integration tests.
    nats-server
    # Native bridge development and official SDK protocol tests.
    nodejs
  ];
  env = [
    {
      name = "NIX_PATH";
      value = "nixpkgs=${toString pkgs.path}";
    }
    {
      name = "NIX_DIR";
      eval = "$PRJ_ROOT/nix";
    }
    {
      name = "GOPATH";
      eval = "$PRJ_ROOT/.go";
    }
    {
      name = "GOMODCACHE";
      eval = "$PRJ_ROOT/.go/pkg/mod";
    }
    {
      name = "GOCACHE";
      eval = "$PRJ_ROOT/.go/build";
    }
  ];
}
