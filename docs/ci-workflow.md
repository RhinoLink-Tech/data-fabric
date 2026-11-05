# CI workflow

Pull requests and pushes to master run the required Build and checks job. It
runs Go formatting, static analysis, unit and deterministic protocol-fixture
tests, package builds, SQL translation checks, and API/skill validation. It
does not start the full external database/version matrix or build the container
image.

The complete provider/version contracts, TLS checks, race tests, container
image build, and image smoke tests run daily at 02:17 China Standard Time. To
run the full suite on demand, open Actions, select Go CI, choose Run workflow,
and run it against master. This full workflow retains the existing live service
contracts; they are deferred from every pull request rather than removed.

A newer commit to the same pull request cancels its superseded in-progress CI
run.
