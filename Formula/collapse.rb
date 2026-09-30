# Builds collapse from the latest source on the main branch. The formula has
# no stable release, only HEAD, so it never needs a version or checksum update.
class Collapse < Formula
  desc "Collapse lines of piped output that match given substrings into one line"
  homepage "https://github.com/ziqul/collaps-output-by-pattern"
  head "https://github.com/ziqul/collaps-output-by-pattern.git", branch: "main"

  depends_on "go" => :build

  def install
    system "go", "build", *std_go_args(ldflags: "-s -w")
  end

  test do
    assert_match "Usage: collapse", shell_output("#{bin}/collapse -h 2>&1")
  end
end
