{ flake, pkgs, ... }:
pkgs.callPackage ./package.nix {
  src = flake;
  runtimes = pkgs.callPackage ./runtimes.nix { src = flake; };
}
