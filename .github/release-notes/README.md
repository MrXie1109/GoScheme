# Release notes

The `release` job in `.github/workflows/ci.yml` runs when a `v*` tag is pushed.
It builds the six static binaries, smoke-tests the one it can run, and creates
the GitHub release:

* if `release-notes/<tag>.md` exists (this directory), its contents become the
  release notes;
* otherwise the release is created with `--generate-notes`, which lists the
  merged pull requests and commits since the last tag;
* if a release for the tag already exists, the assets are re-uploaded with
  `--clobber` instead, so re-running a tag rebuilds the artifacts in place.

So the release process is: write the notes here if the change deserves prose,
commit, then `git tag -a vX.Y.Z && git push origin vX.Y.Z`.  Nothing is built
or uploaded by hand, and no cross toolchain is needed anywhere: the test matrix
already runs the suites on Linux amd64/arm64, macOS amd64/arm64 and Windows
natively, which is what the emulators and wine prefixes in a local checkout
used to be for.

`v2.5.0.md` is kept as an example of the prose notes.
