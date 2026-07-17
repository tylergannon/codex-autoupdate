# Releasing

The public bootstrap at `main/install.sh` always installs the latest tagged module release with `@latest`. Ordinary releases must not edit an installer version string because none exists.

For each release:

1. Run the repository gates and installer proof, then squash-merge the release PR.
2. Tag the exact merged `origin/main` commit and push the tag.
3. Prove that the public bootstrap resolves the new tag, reports its version, and leaves the LaunchAgent running.

Only when `install.sh` itself changes, recompute `shasum -a 256 install.sh` and update the digest in `llms.txt` in the same commit. README and llms.txt always use the stable `main/install.sh` URL.
