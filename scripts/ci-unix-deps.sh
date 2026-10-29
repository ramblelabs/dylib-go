#!/usr/bin/env bash
set -euo pipefail
# Native test producers and the optional scalar ABI backend.
case "$(uname -s)" in
  Linux)
    sudo apt-get update
    sudo apt-get install -y clang clang-19 lld llvm binutils libffi-dev pkg-config gfortran
    # Clang 18 has incompatible SysV lowering for aligned scalar typedefs.
    # Keep the baseline compiler; qualify this fixture with the fixed release.
    echo 'DYLIB_TYPEDEF_CLANG=clang-19' >> "$GITHUB_ENV"
    # x86 GNU2 TLSDESC code generation was added in Clang 19.
    echo 'DYLIB_TLSDESC_CLANG=clang-19' >> "$GITHUB_ENV"
    ;;
  Darwin)
    for formula in libffi pkgconf; do
      brew list --versions "$formula" >/dev/null || brew install "$formula"
    done
    echo "PKG_CONFIG_PATH=$(brew --prefix libffi)/lib/pkgconfig${PKG_CONFIG_PATH:+:$PKG_CONFIG_PATH}" >> "$GITHUB_ENV"
    if ! command -v llc >/dev/null; then
      brew list --versions llvm@22 >/dev/null || brew install llvm@22
      # Keep the IR producer/reader together without replacing other C tools.
      llvm_bin=$(brew --prefix llvm@22)/bin
      echo "DYLIB_LLC=$llvm_bin/llc" >> "$GITHUB_ENV"
      echo "DYLIB_LLVM_CLANG=$llvm_bin/clang" >> "$GITHUB_ENV"
    fi
    # Runner images may provide a versioned gfortran executable only.
    fc=$(find "$(brew --prefix)/bin" -maxdepth 1 -name 'gfortran*' | sort | head -n 1)
    if [[ -z "$fc" ]]; then
      brew install gcc
      fc=$(find "$(brew --prefix)/bin" -maxdepth 1 -name 'gfortran*' | sort | head -n 1)
    fi
    test -n "$fc"
    echo "DYLIB_FC=$fc" >> "$GITHUB_ENV"
    bash scripts/ci-abibridge.sh
    ;;
  *) exit 1 ;;
esac
