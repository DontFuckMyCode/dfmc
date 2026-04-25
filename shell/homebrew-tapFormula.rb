# frozen_string_literal: true

class Dfmc < Formula
  desc "DFMC — Code intelligence assistant with provider routing, tree-sitter AST, and autonomous plan/execute"
  homepage "https://github.com/dontfuckmycode/dfmc"
  license "Apache-2.0"
  version "dev"

  on_macos do
    on_intel do
      url "https://github.com/dontfuckmycode/dfmc/releases/download/v0.0.0/dfmc-darwin-amd64"
      sha256 "use_release_checksum"
    end
    on_arm do
      url "https://github.com/dontfuckmycode/dfmc/releases/download/v0.0.0/dfmc-darwin-arm64"
      sha256 "use_release_checksum"
    end
  end

  bottle do
    root_url "https://github.com/dontfuckmycode/homebrew-tap/releases/download/bottles"
    sha256 cellar: ":any_skip_relocation", arm64_sonoma: "REPLACE_WITH_ARM64_BOTTLES_SHA", intel_sonoma: "REPLACE_WITH_INTEL_BOTTLES_SHA"
  end

  depends_on "go" => :build
  depends_on "gcc" # tree-sitter requires CGO

  def install
    # Build with CGO for full tree-sitter AST support
    ENV["CGO_ENABLED"] = "1"
    system "go", "build", "-ldflags", "-s -w", "-o", bin/"dfmc", "./cmd/dfmc"
  end

  test do
    assert_match "dfmc dev", shell_output("#{bin}/dfmc version").strip
  end
end