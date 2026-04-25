# typed: true
# frozen_string_literal: true

# The dfmc Homebrew formula.
# Maintained at: https://github.com/dontfuckmycode/homebrew-tap
#
# To verify a new release bottle:
#   brew bottle --no-commit homebrew-tapFormula.rb
#   # commit the resulting .bottle.json to the tap repo
#
# To test locally before a release:
#   brew install --build-from-source ./homebrew-tapFormula.rb

class Dfmc < Formula
  desc "Code intelligence assistant — tree-sitter AST, provider router, autonomous Drive"
  homepage "https://github.com/dontfuckmycode/dfmc"
  license "Apache-2.0"
  version "dev"

  #
  # Bottle URLs are filled in at release time by the release workflow
  # (.github/workflows/release.yml populates these from the actual artifacts).
  # Stable installs use the versioned release artifacts.
  #
  on_macos do
    on_intel do
      url "https://github.com/dontfuckmycode/dfmc/releases/download/v0.0.0/dfmc-darwin-amd64"
      sha256 "TAP_REPLACED_AT_RELEASE_TIME"
    end
    on_arm do
      url "https://github.com/dontfuckmycode/dfmc/releases/download/v0.0.0/dfmc-darwin-arm64"
      sha256 "TAP_REPLACED_AT_RELEASE_TIME"
    end
  end

  # Bottles are published by the release workflow; replace the sha256
  # entries above with the actual values from the release artifacts.
  bottle :unneeded

  def install
    bin.install "dfmc"
  end

  test do
    version_output = shell_output("#{bin}/dfmc version")
    assert_match "dfmc", version_output
  end
end