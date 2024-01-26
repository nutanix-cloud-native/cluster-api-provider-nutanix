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
          golangci-lint = pkgs.golangci-lint.override { buildGoModule = buildGo121Module; };

          go-apidiff = buildGo121Module {
            name = "go-apidiff";
            src = fetchFromGitHub {
              owner = "joelanford";
              repo = "go-apidiff";
              rev = "v0.7.0";
              hash = "sha256-vuub9PJ68I5MOYv73NaZTiewPr+4MRdFKQGdfvMi+Dg=";
            };
            doCheck = false;
            subPackages = [ "." ];
            vendorHash = "sha256-GF8mxSVFjaijE8ul9YgMZKaTMTSR5DkwCNY7FZCwiAU=";
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

          go-junit-report = buildGo121Module {
            name = "go-junit-report";
            src = fetchFromGitHub {
              owner = "jstemmer";
              repo = "go-junit-report";
              rev = "v2.1.0";
              sha256 = "sha256-s4XVjACmpd10C5k+P3vtcS/aWxI6UkSUPyxzLhD2vRI=";
            };
            vendorHash = "sha256-+KmC7m6xdkWTT/8MkGaW9gqkzeZ6LWL0DXbt+12iTHY=";
            ldflags = [ "-s" "-w" ];
            meta = with lib; {
              description = "Convert go test output to junit xml";
              homepage = "https://github.com/jstemmer/go-junit-report";
              license = licenses.mit;
              maintainers = with maintainers; [ cryptix ];
            };
			    };

          gocov = buildGo121Module {
            name = "gocov";
            src = fetchFromGitHub {
              owner = "axw";
              repo = "gocov";
              rev = "v1.0.0";
              sha256 = "14dsbabp1h31zzx7xlzg604spk3k3a0wpyq9xsrpqr8hz425h9xv";
            };
            vendorSha256 = "1hkfj18sshn8z0w1njgrwzchagxz1fmpq26a1wsf47xd64ydzwi1";
            meta = with lib; {
              description = "Coverage testing tool for The Go Programming Language.";
              longDescription = ''
                Coverage testing tool for The Go Programming Language.
              '';
              license = licenses.mit;
              broken = false;
              maintainers = with maintainers; [ mpoquet ];
              platforms = platforms.all;
              homepage = "https://github.com/axw/gocov";
            };
          };

          gocov-xml = buildGo121Module {
            name = "gocov-xml";
            src = fetchFromGitHub {
              owner = "AlekSi";
              repo = "gocov-xml";
              rev = "v1.1.0";
              sha256 = "936xrDmxciVr1KpfzzpsztU0XvQGG4fv4v4K2HHSN/A=";
            };
            vendorSha256 = "80fPJXEVj7szTFxxxf5MhWE6Zbll7/SDcdisfZdrOlQ=";
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

          setup-envtest = buildGo121Module {
            name = "setup-envtest";
            src = fetchFromGitHub {
              owner = "kubernetes-sigs";
              repo = "controller-runtime";
              rev = "v0.16.3";
              hash = "sha256-X4YM4A63UxD650S3lxbxRtZaHOyF7LY6d5eVJe91+5c=";
            } + "/tools/setup-envtest";
            doCheck = false;
            subPackages = [ "." ];
            vendorHash = "sha256-ISVGxhFQh4e0eag9Sw0Zj4u1cG0tudZLhJcGdH5tDo4=";
          };
        };

        formatter = alejandra;
      }
    );
}
