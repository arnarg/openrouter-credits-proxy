{
  lib,
  buildGoApplication,
}:
let
  version = "0.1.0";
in
buildGoApplication {
  inherit version;
  pname = "openrouter-credits-proxy";

  src = ./.;
  modules = ./gomod2nix.toml;

  meta = with lib; {
    mainProgram = "openrouter-credits-proxy";
    platforms = platforms.unix;
  };
}
