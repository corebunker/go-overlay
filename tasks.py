from invoke import task, Collection
from collections import Counter
from dotenv import load_dotenv
from typing import Optional
from pathlib import Path
from minio import Minio
import datetime
import shutil
import json
import sys
import os


GO_ENV = {
    "GOBUILD": "go build",
    "GOCLEAN": "go clean",
    "GOTEST": "go test",
    "GOGET": "go get",
    "BINARY_NAME": "go-overlay",
    "BINARY_UNIX": "go-overlay_unix",
    "DOCKER_IMAGE": "go-overlay",
    "DOCKER_TAG": "latest",
}
RELEASE_FILENAME = "go-overlay-linux-amd64"


def _run(c, cmd: str, pty: bool = True, env=None):
    """Helper to run commands."""
    return c.run(cmd, pty=pty, env=env)


def getenv(name: str, default: Optional[str] = None, required: bool = False) -> str:
    """Helper to get environment variables, exiting if required and not found."""
    value = os.getenv(name, default)
    if required and not value:
        print(f"ERROR: Missing required environment variable: {name}")
        sys.exit(1)
    return value or ""


def _analyze_security_reports(reports_dir: Path) -> bool:
    """Analyze Bandit and Gitleaks reports. Return True if passed (no critical issues)."""
    has_critical_issues = False

    bandit_json = reports_dir / "bandit.json"
    if bandit_json.exists():
        print("\n📊 Bandit Security Analysis:")
        try:
            with open(bandit_json, "r", encoding="utf-8") as f:
                bandit_data = json.load(f)
            results = bandit_data.get("results", [])
            if results:
                severities = [r.get("issue_severity", "UNDEFINED") for r in results]
                severity_counts = Counter(severities)
                total = len(results)
                print(f"   🔍 Total findings: {total}")
                for severity in ["HIGH", "MEDIUM", "LOW"]:
                    count = severity_counts.get(severity, 0)
                    if count > 0:
                        emoji = (
                            "🔴"
                            if severity == "HIGH"
                            else ("🟡" if severity == "MEDIUM" else "🔵")
                        )
                        print(f"   {emoji} {severity.capitalize()}: {count}")
                        if severity == "HIGH":
                            has_critical_issues = True
            else:
                print("   ✅ No security issues found!")
        except Exception as e:
            print(f"   ⚠️ Error analyzing Bandit JSON: {e}")

    gitleaks_json = reports_dir / "gitleaks.json"
    if gitleaks_json.exists():
        print("\n🔑 Gitleaks Secret Scanning:")
        try:
            with open(gitleaks_json, "r", encoding="utf-8") as f:
                data = json.load(f)
            if isinstance(data, list) and data:
                print(f"   🚨 Found {len(data)} potential secrets!")
                rule_counts = Counter(
                    [item.get("RuleID", "unknown") for item in data]
                )
                for rule_id, count in rule_counts.most_common():
                    print(f"      • {rule_id}: {count}")
                has_critical_issues = True
            else:
                print("   ✅ No secrets detected!")
        except Exception as e:
            print(f"   ⚠️ Error analyzing Gitleaks JSON: {e}")

    print("\n" + "=" * 50)
    if has_critical_issues:
        print("❌ SECURITY SCAN FAILED - Critical issues found!")
        print("🔧 Review the reports in dist/security/ for details")
    else:
        print("✅ SECURITY SCAN PASSED - No critical issues found!")
    print("=" * 50)

    return not has_critical_issues


# =============================================================================
# Go Tasks
# =============================================================================
@task
def build(c):
    """Build the binary for the local OS."""
    print(f"Building {GO_ENV['BINARY_NAME']}...")
    _run(c, f"{GO_ENV['GOBUILD']} -o {GO_ENV['BINARY_NAME']} -v ./cmd/go-overlay")


@task
def build_linux(c):
    """Build the binary for Linux (for releases and Docker)."""
    print("Building for Linux...")
    env = os.environ.copy()
    env.update({"CGO_ENABLED": "0", "GOOS": "linux"})
    _run(c, f"{GO_ENV['GOBUILD']} -o {GO_ENV['BINARY_UNIX']} -v ./cmd/go-overlay", env=env)


@task
def clean(c):
    """Clean build artifacts."""
    print("Cleaning...")
    _run(c, GO_ENV["GOCLEAN"])
    Path(GO_ENV["BINARY_NAME"]).unlink(missing_ok=True)
    Path(GO_ENV["BINARY_UNIX"]).unlink(missing_ok=True)
    print("Clean complete.")


@task
def test(c):
    """Run unit tests."""
    print("Running unit tests...")
    _run(c, f"{GO_ENV['GOTEST']} -v -timeout 30s ./...")


@task
def test_integration(c):
    """Run integration tests."""
    print("Running integration tests...")
    _run(c, f"{GO_ENV['GOTEST']} -v -timeout 2m -tags=integration ./...")


@task
def test_coverage(c):
    """Run tests with coverage report."""
    print("Running tests with coverage...")
    coverage_dir = Path("coverage")
    coverage_dir.mkdir(parents=True, exist_ok=True)

    _run(c, f"{GO_ENV['GOTEST']} -coverprofile=coverage/coverage.out -covermode=atomic ./...")
    _run(c, "go tool cover -html=coverage/coverage.out -o coverage/coverage.html")
    _run(c, "go tool cover -func=coverage/coverage.out")
    print(f"\n✅ Coverage report generated: coverage/coverage.html")


@task
def test_bench(c):
    """Run benchmark tests."""
    print("Running benchmark tests...")
    _run(c, f"{GO_ENV['GOTEST']} -bench=. -benchmem -run=^# ./...")


@task
def deps(c):
    """Install Go dependencies."""
    print("Installing dependencies...")
    _run(c, f"{GO_ENV['GOGET']} -v ./...")


# =============================================================================
# Code Quality Tasks
# =============================================================================
@task
def lint(c, strict=False):
    """Run golangci-lint."""
    print("Running golangci-lint...")
    # Check if golangci-lint is installed
    result = c.run("mise exec -- which golangci-lint", warn=True, hide=True)
    if result.ok:
        if strict:
            _run(c, "mise exec -- golangci-lint run --timeout 5m")
        else:
            # Run with warnings allowed (only fail on errors)
            result = c.run("mise exec -- golangci-lint run --timeout 5m --max-issues-per-linter=0 --max-same-issues=0", warn=True, pty=True)
            if result.return_code != 0:
                print("⚠️  Linting found issues (see above)")
                print("💡 Run 'invoke quality.lint --strict' to fail on warnings")
            else:
                print("✅ Linting passed")
    else:
        print("⚠️  golangci-lint not installed. Install with:")
        print("    invoke tools")
        print("    or manually: mise exec -- go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest")
        sys.exit(1)


@task
def fmt(c):
    """Format Go code."""
    print("Formatting code...")
    _run(c, "gofmt -s -w .")

    # Try gofumpt if available
    result = c.run("command -v gofumpt", warn=True, hide=True)
    if result.ok:
        _run(c, "gofumpt -l -w .")

    # Try goimports if available
    result = c.run("command -v goimports", warn=True, hide=True)
    if result.ok:
        _run(c, "goimports -w .")

    print("✅ Code formatted")


@task
def vet(c):
    """Run go vet."""
    print("Running go vet...")
    _run(c, "go vet ./...")
    print("✅ go vet passed")


@task
def check(c):
    """Run all checks: fmt, vet, lint, test."""
    print("\n" + "=" * 60)
    print("🔍 RUNNING ALL CHECKS")
    print("=" * 60 + "\n")

    fmt(c)
    print()
    vet(c)
    print()
    lint(c)
    print()
    test(c)

    print("\n✅ All checks passed!")


# =============================================================================
# Pre-commit Tasks
# =============================================================================
@task
def precommit_install(c):
    """Install pre-commit hooks."""
    print("Installing pre-commit hooks...")
    result = c.run("command -v pre-commit", warn=True, hide=True)
    if result.ok:
        _run(c, "pre-commit install")
        _run(c, "pre-commit install --hook-type commit-msg")
        print("✅ Pre-commit hooks installed")
    else:
        print("⚠️  pre-commit not installed. Install with:")
        print("    pip install pre-commit")
        sys.exit(1)


@task
def precommit_run(c):
    """Run pre-commit hooks on all files."""
    print("Running pre-commit hooks...")
    result = c.run("command -v pre-commit", warn=True, hide=True)
    if result.ok:
        _run(c, "pre-commit run --all-files")
    else:
        print("⚠️  pre-commit not installed")
        sys.exit(1)


@task
def precommit(c, skip_integration=False, strict_lint=False):
    """Run all pre-commit checks: format, lint, unit tests (fast check before commit)."""
    print("\n" + "=" * 60)
    print("🚀 PRE-COMMIT CHECKS")
    print("=" * 60 + "\n")

    start_time = datetime.datetime.now()

    try:
        print("📝 Step 1/5: Formatting code...")
        fmt(c)

        print("\n🔍 Step 2/5: Running go vet...")
        vet(c)

        print("\n🔎 Step 3/5: Running linter...")
        lint(c, strict=strict_lint)

        print("\n🧪 Step 4/5: Running unit tests...")
        test(c)

        if not skip_integration:
            print("\n🧩 Step 5/5: Running integration tests...")
            test_integration(c)
        else:
            print("\n⚠️  Step 5/5: Integration tests skipped")

        end_time = datetime.datetime.now()
        duration = (end_time - start_time).total_seconds()

        print("\n" + "=" * 60)
        print(f"✅ PRE-COMMIT CHECKS PASSED!")
        print(f"⏱️  Total time: {duration:.2f}s")
        print("=" * 60)
        print("\n💡 Ready to commit! Run: git add . && git commit -m 'your message'\n")

    except Exception as e:
        end_time = datetime.datetime.now()
        duration = (end_time - start_time).total_seconds()

        print("\n" + "=" * 60)
        print(f"❌ PRE-COMMIT CHECKS FAILED!")
        print(f"⏱️  Time to failure: {duration:.2f}s")
        print(f"💥 Error: {e}")
        print("=" * 60)
        print("\n🔧 Fix the issues above before committing\n")
        raise


@task
def precommit_fast(c):
    """Fast pre-commit check: format, vet, unit tests only (skip lint and integration)."""
    print("\n" + "=" * 60)
    print("⚡ FAST PRE-COMMIT CHECKS")
    print("=" * 60 + "\n")

    start_time = datetime.datetime.now()

    try:
        print("📝 Step 1/3: Formatting code...")
        fmt(c)

        print("\n🔍 Step 2/3: Running go vet...")
        vet(c)

        print("\n🧪 Step 3/3: Running unit tests...")
        test(c)

        end_time = datetime.datetime.now()
        duration = (end_time - start_time).total_seconds()

        print("\n" + "=" * 60)
        print(f"✅ FAST PRE-COMMIT CHECKS PASSED!")
        print(f"⏱️  Total time: {duration:.2f}s")
        print("=" * 60)
        print("\n💡 Ready to commit! Run: git add . && git commit -m 'your message'\n")

    except Exception as e:
        end_time = datetime.datetime.now()
        duration = (end_time - start_time).total_seconds()

        print("\n" + "=" * 60)
        print(f"❌ FAST PRE-COMMIT CHECKS FAILED!")
        print(f"⏱️  Time to failure: {duration:.2f}s")
        print(f"💥 Error: {e}")
        print("=" * 60)
        print("\n🔧 Fix the issues above before committing\n")
        raise


@task
def tools(c):
    """Install development tools using mise."""
    print("Installing development tools...")

    print("\n📦 Installing golangci-lint...")
    c.run("mise exec -- go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest", pty=True)

    print("\n📦 Installing gosec...")
    c.run("mise exec -- go install github.com/securego/gosec/v2/cmd/gosec@latest", pty=True)

    print("\n📦 Installing gofumpt...")
    c.run("mise exec -- go install mvdan.cc/gofumpt@latest", pty=True)

    print("\n📦 Installing goimports...")
    c.run("mise exec -- go install golang.org/x/tools/cmd/goimports@latest", pty=True)

    print("\n📦 Installing staticcheck...")
    c.run("mise exec -- go install honnef.co/go/tools/cmd/staticcheck@latest", pty=True)

    print("\n✅ All development tools installed!")
    print("\n💡 Tools are installed in mise's Go environment")
    print("   Use 'mise exec -- <command>' to run them")


# =============================================================================
# Installation Tasks
# =============================================================================
@task(pre=[build])
def install(c):
    """Install the binary in the system."""
    print("Installing go-overlay...")
    c.run(f"sudo cp {GO_ENV['BINARY_NAME']} /go-overlay", pty=True)
    c.run(f"sudo chmod +x /go-overlay", pty=True)
    print("✓ go-overlay installed successfully!")
    print("Run 'go-overlay --help' to see commands.")


@task
def uninstall(c):
    """Uninstall the binary from the system."""
    print("Uninstalling go-overlay...")
    c.run("sudo rm -f /go-overlay", pty=True)
    c.run("sudo rm -f /usr/local/bin/entrypoint", pty=True, warn=True)
    c.run("sudo rm -rf /etc/go-overlay", pty=True, warn=True)
    print("✓ go-overlay uninstalled")


# =============================================================================
# Docker Tasks
# =============================================================================
@task(pre=[build_linux])
def docker_build(c):
    """Build the Docker image using the local binary."""
    print("Building Docker image...")
    _run(c, f"docker build -t {GO_ENV['DOCKER_IMAGE']}:{GO_ENV['DOCKER_TAG']} .")


@task
def docker_release(c, version="latest"):
    """Build a release Docker image from GitHub."""
    tag = f"release-{version}" if version != "latest" else "release-latest"
    print(f"Building Docker image from GitHub release ({version})...")
    build_arg = f"--build-arg VERSION={version}" if version != "latest" else ""
    _run(
        c,
        f"docker build -f Dockerfile.release {build_arg} -t {GO_ENV['DOCKER_IMAGE']}:{tag} .",
    )


@task(help={"daemon": "Run in daemon mode (background)", "port": "Port to expose"})
def docker_run(c, daemon=False, port=80):
    """Run the Docker container."""
    container_name = "go-overlay-test"
    # Stop if already running
    c.run(f"docker rm -f {container_name} || true", pty=True, warn=True, hide=True)

    run_options = "-d" if daemon else "--rm"
    mode = "in background" if daemon else "in foreground"
    print(f"Running Docker container {mode}...")
    _run(
        c,
        f"docker run {run_options} -p {port}:{port} --name {container_name} {GO_ENV['DOCKER_IMAGE']}:{GO_ENV['DOCKER_TAG']}",
    )


@task
def docker_stop(c):
    """Stop and remove the test Docker container."""
    container_name = "go-overlay-test"
    print("Stopping Docker container...")
    c.run(f"docker stop {container_name} || true", pty=True, warn=True)
    c.run(f"docker rm {container_name} || true", pty=True, warn=True)


@task
def docker_logs(c):
    """Show logs from the test Docker container."""
    container_name = "go-overlay-test"
    print("Showing Docker container logs...")
    _run(c, f"docker logs -f {container_name}")


# =============================================================================
# Release & Test Tasks
# =============================================================================
@task(pre=[clean, test, build_linux])
def release(c):
    """Create a release package."""
    print("Creating release package...")
    release_dir = Path("release")
    release_dir.mkdir(parents=True, exist_ok=True)
    shutil.copy(GO_ENV["BINARY_UNIX"], release_dir / RELEASE_FILENAME)

    if Path("VERSION").exists():
        shutil.copy("VERSION", release_dir / "VERSION")

    shutil.copy("services.toml", release_dir)
    if Path("tests/install-enhanced.sh").exists():
        shutil.copy("tests/install-enhanced.sh", release_dir)
    shutil.copy("README.md", release_dir)
    print(f"✓ Release created in ./{release_dir}/")


@task(pre=[release])
def upload(c):
    """Upload the release artifact to Backblaze B2."""
    print("Uploading artifact to Backblaze B2...")

    load_dotenv(dotenv_path=os.getenv("DOTENV_PATH", ".env"))
    endpoint = getenv("BACKBLAZE_ENDPOINT", "s3.us-east-005.backblazeb2.com")
    access_key = getenv("BACKBLAZE_ACCESS_KEY_ID", required=True)
    secret_key = getenv("BACKBLAZE_SECRET_ACCESS_KEY", required=True)
    bucket_name = getenv("BACKBLAZE_BUCKET", required=True)

    file_path = Path("release") / RELEASE_FILENAME
    if not file_path.is_file():
        print(f"ERROR: File not found: {file_path}")
        sys.exit(1)

    object_name = getenv("OBJECT_NAME", file_path.name)
    expiration_str = getenv("EXPIRATION", "3600")
    try:
        expiration_seconds = int(expiration_str)
    except ValueError:
        print(f"ERROR: Invalid EXPIRATION value: {expiration_str}")
        sys.exit(1)

    client = Minio(
        endpoint,
        access_key=access_key,
        secret_key=secret_key,
        secure=True,
    )

    try:
        found = client.bucket_exists(bucket_name)
        if not found:
            print(f"ERROR: Bucket '{bucket_name}' not found.")
            sys.exit(1)

        result = client.fput_object(
            bucket_name,
            object_name,
            str(file_path),
        )
        print(
            f"✅ Upload complete: {result.object_name} (version: {result.version_id}) -> bucket {bucket_name}"
        )

        url = client.presigned_get_object(
            bucket_name,
            object_name,
            expires=datetime.timedelta(seconds=expiration_seconds),
        )
        print(f"🔗 Presigned URL (valid for {expiration_seconds // 60} min):\n{url}")

        try:
            cluster_id = endpoint.split(".")[1].split("-")[-1]  # e.g., "005"
            friendly_domain = f"f{cluster_id}.backblazeb2.com"
            friendly_url = f"https://{friendly_domain}/file/{bucket_name}/{object_name}"
            print(f"🌐 Public URL (if bucket is public):\n{friendly_url}")
        except Exception as parse_err:
            print(f"⚠️ Could not generate public URL automatically: {parse_err}")

    except Exception as e:
        print(f"❌ Upload or URL generation error: {e}")
        sys.exit(1)


# =============================================================================
# Security Scan Tasks
# =============================================================================
@task(
    help={
        "gitleaks_image": "Docker image for gitleaks (default: ghcr.io/gitleaks/gitleaks:latest)",
        "bandit_image": "Docker image for bandit (default: ghcr.io/pycqa/bandit/bandit)",
    }
)
def security_scan(
    c,
    gitleaks_image: str = "ghcr.io/gitleaks/gitleaks:latest",
    bandit_image: str = "ghcr.io/pycqa/bandit/bandit",
):
    """Run Bandit and Gitleaks scans inside Docker and fail on critical issues."""
    print("\n🛡️  Running security scans...")
    cwd = os.getcwd()
    reports_dir = Path("dist/security")
    reports_dir.mkdir(parents=True, exist_ok=True)

    print("\n🔎 Running Bandit security analysis...")
    bandit_report = reports_dir / "bandit.json"
    bandit_cmd = (
        f"docker run --rm -v '{cwd}:/src' {bandit_image} "
        f"-r -f json -o /src/{bandit_report} /src"
    )
    c.run(bandit_cmd, pty=True, warn=True)

    if bandit_report.exists():
        try:
            with open(bandit_report, "r", encoding="utf-8") as f:
                report = json.load(f)
            highs = [
                r
                for r in report.get("results", [])
                if r.get("issue_severity") == "HIGH"
            ]
            if highs:
                print("🔴 High severity issues found by Bandit. Failing scan...")
                raise SystemExit("Bandit found high severity issues.")
        except Exception as e:
            print(f"⚠️ Could not parse Bandit report: {e}")

    print("\n🔑 Running Gitleaks secret scanning...")
    gitleaks_json = reports_dir / "gitleaks.json"
    gitleaks_sarif = reports_dir / "gitleaks.sarif"
    base_cmd = "gitleaks detect --source ."
    gl_json_cmd = f"docker run --rm -v '{cwd}:/work' -w /work {gitleaks_image} {base_cmd} --report-format json --report-path {gitleaks_json}"
    gl_sarif_cmd = f"docker run --rm -v '{cwd}:/work' -w /work {gitleaks_image} {base_cmd} --report-format sarif --report-path {gitleaks_sarif}"
    c.run(gl_json_cmd, pty=True, warn=True)
    c.run(gl_sarif_cmd, pty=True, warn=True)

    scan_passed = _analyze_security_reports(reports_dir)
    if not scan_passed:
        raise SystemExit("Security scan failed - critical issues found!")

    print("✅ Security scans completed successfully.")


@task
def security_summary(c):
    """Display summary of security scan results from dist/security/."""
    reports_dir = Path("dist/security")
    if not reports_dir.exists():
        print("❌ No security reports found. Run 'invoke security.scan' first.")
        return
    _analyze_security_reports(reports_dir)


@task
def security_gosec(c):
    """Run gosec static analysis on Go code and fail on findings."""
    print("\n🛡️  Running gosec (Go SAST)...")
    reports_dir = Path("dist/security")
    reports_dir.mkdir(parents=True, exist_ok=True)
    report_path = reports_dir / "gosec.json"

    c.run("mise exec -- go install github.com/securego/gosec/v2/cmd/gosec@latest", pty=True)

    min_sev = os.getenv("GOSEC_MIN_SEVERITY", "HIGH").upper()
    exclude_rules = os.getenv("GOSEC_EXCLUDE_RULES", "").strip()
    exclude_dirs = os.getenv("GOSEC_EXCLUDE_DIRS", "").strip()

    gosec_flags = ["-fmt=json", f"-out {report_path}"]
    if exclude_rules:
        gosec_flags.append(f"-exclude={exclude_rules}")
    if exclude_dirs:
        for d in [d.strip() for d in exclude_dirs.split(",") if d.strip()]:
            gosec_flags.append(f"-exclude-dir={d}")

    flags_str = " ".join(gosec_flags)

    c.run(
        f"mise exec -- gosec {flags_str} ./...",
        pty=True,
        warn=True,
    )

    try:
        if report_path.exists():
            with open(report_path, "r", encoding="utf-8") as f:
                data = json.load(f)
            issues = data.get("Issues", []) if isinstance(data, dict) else []
            if issues:
                sev_counts = Counter(
                    [i.get("severity", "UNKNOWN").upper() for i in issues]
                )
                print("\n📊 gosec findings:")
                for k, v in sev_counts.items():
                    print(f"   • {k}: {v}")

                order = {"LOW": 1, "MEDIUM": 2, "HIGH": 3}
                threshold = order.get(min_sev, 3)
                max_issue_level = 0
                for issue in issues:
                    lvl = order.get(issue.get("severity", "UNKNOWN").upper(), 0)
                    if lvl > max_issue_level:
                        max_issue_level = lvl

                if max_issue_level >= threshold:
                    raise SystemExit("gosec found issues at or above threshold")
                else:
                    print(
                        f"✅ gosec: no issues at or above threshold (min={min_sev})"
                    )
            else:
                print("✅ gosec: no issues found")
        else:
            print("⚠️ gosec report not generated; treating as pass")
    except Exception as e:
        print(f"⚠️ Could not parse gosec report: {e}")
        raise SystemExit("gosec scan failed")


@task
def security_govulncheck(c):
    """Run govulncheck to detect known vulnerabilities in dependencies."""
    print("\n🛡️  Running govulncheck (Go dependency vulnerabilities)...")
    reports_dir = Path("dist/security")
    reports_dir.mkdir(parents=True, exist_ok=True)
    report_path = reports_dir / "govulncheck.json"

    c.run("mise exec -- go install golang.org/x/vuln/cmd/govulncheck@latest", pty=True)

    c.run(
        f"mise exec -- govulncheck -json ./... > {report_path}",
        pty=True,
        warn=True,
    )

    try:
        if report_path.exists():
            vulns = 0
            with open(report_path, "r", encoding="utf-8") as f:
                for line in f:
                    line = line.strip()
                    if not line:
                        continue
                    try:
                        obj = json.loads(line)
                        if obj.get("Type") == "vulnerability":
                            vulns += 1
                    except Exception:
                        continue
            if vulns > 0:
                print(f"🔴 govulncheck: found {vulns} vulnerabilities")
                raise SystemExit("govulncheck found vulnerabilities")
            else:
                print("✅ govulncheck: no vulnerabilities found")
        else:
            print("⚠️ govulncheck report not generated; treating as pass")
    except Exception as e:
        print(f"⚠️ Could not parse govulncheck report: {e}")
        raise SystemExit("govulncheck scan failed")


@task
def security_go(c):
    """Run both gosec and govulncheck."""
    security_gosec(c)
    security_govulncheck(c)


@task
def test_cli(c):
    """Run CLI tests using Docker."""
    print("Testing CLI commands...")
    docker_run(c, daemon=True)
    c.run("sleep 5", pty=True)

    container_name = "go-overlay-test"
    print(f"\n--- Testing go-overlay commands on container {container_name} ---")
    commands = {
        "Status": "status",
        "List services": "list",
        "Restart nginx": "restart nginx",
    }
    for desc, cmd in commands.items():
        print(f"\n--> {desc}...")
        c.run(f"docker exec {container_name} go-overlay {cmd}", pty=True, warn=True)
        if "restart" in cmd:
            print("Waiting 3s after restart...")
            c.run("sleep 3", pty=True)
            print("--> Listing services again...")
            c.run(f"docker exec {container_name} go-overlay list", pty=True, warn=True)

    print("\n--- CLI tests finished ---")
    docker_stop(c)


# =============================================================================
# CI/CD Pipeline Tasks
# =============================================================================


@task
def ci_test(c):
    """Run all tests (unit + integration + coverage)."""
    print("\n" + "=" * 60)
    print("🧪 STAGE 1/4: RUNNING TESTS")
    print("=" * 60 + "\n")

    print("📦 Installing dependencies...")
    deps(c)

    print("\n🔬 Running unit tests...")
    test(c)

    print("\n🧩 Running integration tests...")
    test_integration(c)

    print("\n📊 Generating coverage report...")
    test_coverage(c)

    print("\n✅ All tests passed!")


@task
def ci_security(c):
    """Run all security scans (gosec + govulncheck)."""
    print("\n" + "=" * 60)
    print("🔒 STAGE 2/4: SECURITY SCANNING")
    print("=" * 60 + "\n")

    print("🛡️  Running gosec (static analysis)...")
    security_gosec(c)

    print("\n🔍 Running govulncheck (vulnerabilities)...")
    security_govulncheck(c)

    print("\n✅ All security scans passed!")


@task
def ci_build(c):
    """Build the binary for release."""
    print("\n" + "=" * 60)
    print("🔨 STAGE 3/4: BUILD")
    print("=" * 60 + "\n")

    print("🏗️  Building binary for Linux...")
    build_linux(c)

    print("\n✅ Build completed!")


@task
def ci_full(c):
    """Run complete CI pipeline: test -> security -> build."""
    print("\n" + "=" * 60)
    print("🚀 STARTING FULL CI PIPELINE")
    print("=" * 60 + "\n")

    start_time = datetime.datetime.now()

    try:
        ci_test(c)
        ci_security(c)
        ci_build(c)

        end_time = datetime.datetime.now()
        duration = (end_time - start_time).total_seconds()

        print("\n" + "=" * 60)
        print(f"✅ FULL CI PIPELINE - SUCCESS!")
        print(f"⏱️  Total time: {duration:.2f}s")
        print("=" * 60 + "\n")

    except Exception as e:
        end_time = datetime.datetime.now()
        duration = (end_time - start_time).total_seconds()

        print("\n" + "=" * 60)
        print(f"❌ CI PIPELINE FAILED!")
        print(f"⏱️  Time to failure: {duration:.2f}s")
        print(f"💥 Error: {e}")
        print("=" * 60 + "\n")
        raise


@task
def cd_release(c, skip_tests=False):
    """Run complete CD pipeline: (optional tests) -> release -> upload."""
    print("\n" + "=" * 60)
    print("🚀 STARTING CD PIPELINE (RELEASE)")
    print("=" * 60 + "\n")

    start_time = datetime.datetime.now()

    try:
        if not skip_tests:
            print("🧪 Running tests before release...")
            ci_test(c)
            ci_security(c)
        else:
            print("⚠️  WARNING: Tests skipped (skip_tests=True)")

        print("\n" + "=" * 60)
        print("📦 STAGE 4/4: CREATING RELEASE")
        print("=" * 60 + "\n")

        print("📦 Cleaning old artifacts...")
        clean(c)

        print("\n🏗️  Building and packaging release...")
        release(c)

        if os.getenv("BACKBLAZE_ACCESS_KEY_ID"):
            print("\n☁️  Uploading to Backblaze B2...")
            upload(c)
        else:
            print("\n⚠️  B2 upload skipped (variables not configured)")

        end_time = datetime.datetime.now()
        duration = (end_time - start_time).total_seconds()

        print("\n" + "=" * 60)
        print(f"✅ FULL CD PIPELINE - SUCCESS!")
        print(f"⏱️  Total time: {duration:.2f}s")
        print("=" * 60 + "\n")

    except Exception as e:
        end_time = datetime.datetime.now()
        duration = (end_time - start_time).total_seconds()

        print("\n" + "=" * 60)
        print(f"❌ CD PIPELINE FAILED!")
        print(f"⏱️  Time to failure: {duration:.2f}s")
        print(f"💥 Error: {e}")
        print("=" * 60 + "\n")
        raise


@task
def ci_quick(c):
    """Quick CI check: test + build (skip security scans)."""
    print("\n" + "=" * 60)
    print("⚡ QUICK CI PIPELINE (skip security scans)")
    print("=" * 60 + "\n")

    start_time = datetime.datetime.now()

    try:
        ci_test(c)
        ci_build(c)

        end_time = datetime.datetime.now()
        duration = (end_time - start_time).total_seconds()

        print("\n" + "=" * 60)
        print(f"✅ QUICK CI PIPELINE - SUCCESS!")
        print(f"⏱️  Total time: {duration:.2f}s")
        print("=" * 60 + "\n")

    except Exception as e:
        end_time = datetime.datetime.now()
        duration = (end_time - start_time).total_seconds()

        print("\n" + "=" * 60)
        print(f"❌ QUICK CI PIPELINE FAILED!")
        print(f"⏱️  Time to failure: {duration:.2f}s")
        print(f"💥 Error: {e}")
        print("=" * 60 + "\n")
        raise


# =============================================================================
# Main Namespace Configuration
# =============================================================================
go_coll = Collection("go", build, build_linux, clean, test, deps)

test_coll = Collection(
    "test",
    test,
    test_integration,
    test_coverage,
    test_bench,
)

quality_coll = Collection(
    "quality",
    lint,
    fmt,
    vet,
    check,
)

precommit_coll = Collection(
    "precommit",
    precommit_install,
    precommit_run,
    precommit,
    precommit_fast,
)

docker_coll = Collection(
    "docker", docker_build, docker_release, docker_run, docker_stop, docker_logs
)
release_coll = Collection("release", release, upload)
security_coll = Collection(
    "security",
    security_scan,
    security_summary,
    security_gosec,
    security_govulncheck,
    security_go,
)
ci_coll = Collection("ci", ci_test, ci_security, ci_build, ci_full, ci_quick)
cd_coll = Collection("cd", cd_release)

ns = Collection(
    go_coll,
    test_coll,
    quality_coll,
    precommit_coll,
    docker_coll,
    release_coll,
    security_coll,
    ci_coll,
    cd_coll,
    install,
    uninstall,
    test_cli,
    tools,
)
