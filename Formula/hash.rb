class Hash < Formula
  desc "AI-powered shell with ACP integration"
  homepage "https://github.com/tfcace/hash"
  url "https://github.com/tfcace/hash/archive/refs/tags/v0.2.0.tar.gz"
  sha256 "013eb8bc35e0a20c0a996df3aabafbba410c725b104b9f5c52d730f36d61c3ab"
  license "MIT"

  depends_on "go" => :build

  def install
    commit = `git rev-parse --short HEAD`.strip
    ldflags = %W[
      -X github.com/tfcace/hash/internal/version.Version=#{version}
      -X github.com/tfcace/hash/internal/version.GitCommit=#{commit}
      -X github.com/tfcace/hash/internal/version.JjChange=unknown
      -X github.com/tfcace/hash/internal/version.BuildDate=#{Time.now.utc.strftime("%Y-%m-%d")}
    ]
    system "go", "build", *std_go_args(ldflags:), "./cmd/hash"
  end

  def caveats
    <<~EOS
      To use hash as your login shell:
        sudo sh -c 'echo #{opt_bin}/hash >> /etc/shells'
        chsh -s #{opt_bin}/hash
    EOS
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/hash --version")
  end
end
