# Releasing

For each release:

1. Bump `default_version` in `install.sh` and the matching expectations in `internal/launchagent/install_script_test.go`.
2. Update the versioned `install.sh` URLs in `README.md` and `llms.txt`.
3. Run `shasum -a 256 install.sh` and update the digest in `llms.txt`.
4. Run the repository gates and installer proof, then squash-merge the release PR.
5. Tag the exact merged `origin/main` commit and push the tag.
6. From fresh Go caches, prove the public tagged `go install`; then run the checksum-verified agent path and the short `curl | bash` path.
7. Verify the installed version, canonical executable, plist arguments, LaunchAgent status, and read-only `check` output.
