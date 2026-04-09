{
  description = "Useful flakes for golang and Kubernetes projects";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs = inputs @ { self, nixpkgs, flake-utils }:
    flake-utils.lib.eachDefaultSystem (system:
      with nixpkgs.legacyPackages.${system}; rec {
        packages = rec {
          go-apidiff = buildGo121Module {
            name = "go-apidiff";
            src = fetchFromGitHub {
              owner = "joelanford";
              repo = "go-apidiff";
              rev = "v0.8.2";
              hash = "sha256-YxxOemAvseRJSeCDdwuBnzjpmK3tUXt6BJGjJx5t4zQ";
            };
            doCheck = false;
            subPackages = [ "." ];
            vendorHash = "sha256-AOIOjfQldAsINbcGkpM/fnyTVEZzzOXM0JnqAVwwdm8=";
          };

          go-mod-upgrade = buildGo121Module {
            name = "go-mod-upgrade";
            src = fetchFromGitHub {
              owner = "oligot";
              repo = "go-mod-upgrade";
              rev = "v0.9.1";
              hash = "sha256-+C0IMb7MU1fq/P0/tTUNmzznZ1q5M69491pO5yBZlVs=";
            };
            doCheck = false;
            subPackages = [ "." ];
            vendorHash = "sha256-8rbRxtOiKmnf68kjsUCXaZf+MHI1n5aXa91Aneq9SKo=";
          };

          yamllint-checkstyle = buildGo121Module {
            pname = "yamllint-checkstyle";
            name = "yamllint-checkstyle";
            src = fetchFromGitHub {
              owner = "thomaspoignant";
              repo = "yamllint-checkstyle";
              rev = "v1.0.2";
              sha256 = "jdgzR+q7IiEpZid0/L6rtkKD8d6DvN48rfJZ+EN+xB0=";
            };
            vendorHash = "sha256-LHRd8Q/v3ceFOqULsTtphfd4xBsz3XBG4Rkmn3Ty6CE=";
          };

          setup-envtest = buildGo122Module {
            name = "setup-envtest";
            src = fetchFromGitHub {
              owner = "kubernetes-sigs";
              repo = "controller-runtime";
              rev = "v0.19.0";
              hash = "sha256-9AqZMiA+OIJD+inmeUc/lq57kV7L85jk1I4ywiSKirg=";
            } + "/tools/setup-envtest";
            doCheck = false;
            subPackages = [ "." ];
            vendorHash = "sha256-sn3HiKTpQzjrFTOVOGFJwoNpxU+XWgkWD2EOcPilePY=";
          };
        };

        formatter = alejandra;
      }
    );
}
