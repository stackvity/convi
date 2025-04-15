# Contributing to convi

Thank you for your interest in contributing to `convi`! We welcome contributions from the community. Please follow these guidelines to ensure a smooth process.

## Reporting Bugs

1.  **Search Existing Issues:** Before reporting a new bug, please search the [GitHub Issues](https://github.com/stackvity/convi/issues) to see if a similar issue has already been reported.
2.  **Create a New Issue:** If the bug hasn't been reported, create a new issue. Please include:
    - A clear and descriptive title.
    - The version of `convi` you are using (`convi --version`).
    - Your operating system and version.
    - The exact command you ran.
    - Any relevant configuration file (`convi.yaml`) snippets.
    - Clear steps to reproduce the bug.
    - The actual behavior you observed.
    - The expected behavior.
    - Any relevant error messages or console output (use code blocks).
    - If possible, include verbose logs (`convi ... -v`).

## Proposing Changes (Features, Enhancements)

1.  **Search Existing Issues:** Check if your idea has already been discussed or proposed in [GitHub Issues](https://github.com/stackvity/convi/issues).
2.  **Create a New Issue:** Create a new issue to discuss your proposed change. Explain the motivation and intended implementation. This allows maintainers and the community to provide feedback before significant work is done.
3.  **Discuss:** Engage in the discussion on the issue tracker.

## Development Setup

1.  **Prerequisites:**
    - [Go](https://go.dev/doc/install) (Version specified in `go.mod`, e.g., >= 1.19)
    - [Git](https://git-scm.com/book/en/v2/Getting-Started-Installing-Git)
    - `make` (usually pre-installed on Linux/macOS, available via tools like Chocolatey or WSL on Windows)
2.  **Clone the Repository:**
    ```bash
    git clone https://github.com/stackvity/convi.git
    cd convi
    ```
3.  **Install Development Tools:** Run `make install-deps` to install necessary Go tools like `golangci-lint` and `goimports`. Ensure `$GOPATH/bin` or `$HOME/go/bin` is in your system's `PATH`.
    ```bash
    make install-deps
    ```
4.  **Build:** Build the project using the Makefile.
    ```bash
    make build
    ```
    The executable will be `./convi` (or `convi.exe` on Windows).
5.  **Verify:** Run basic commands.
    ```bash
    ./convi --version
    ./convi --help
    ```

## Submitting Pull Requests (PRs)

1.  **Fork the Repository:** Create a fork of `github.com/stackvity/convi` on GitHub.
2.  **Clone Your Fork:** Clone your forked repository locally.
3.  **Create a Branch:** Create a new branch for your changes, usually based on `main`. Use a descriptive name (e.g., `feat/add-xml-support`, `fix/cache-invalidation-bug`).
    ```bash
    git checkout -b feat/your-new-feature main
    ```
4.  **Make Changes:** Implement your feature or bug fix.
5.  **Code Quality:**
    - **Format:** Ensure your code is formatted using `gofmt` and `goimports`. Run `make format`.
    - **Lint:** Ensure your code passes lint checks. Run `make lint`.
    - **Tests:** Write unit and/or E2E tests for your changes. Ensure all tests pass (including race detector). Run `make test`.
6.  **Commit Changes:** Make small, logical commits with clear commit messages. [Conventional Commits](https://www.conventionalcommits.org/) are encouraged but not strictly required.
7.  **Push Changes:** Push your branch to your fork on GitHub.
    ```bash
    git push origin feat/your-new-feature
    ```
8.  **Create Pull Request:** Go to the original `stackvity/convi` repository on GitHub and create a Pull Request from your branch to the `main` branch.
9.  **PR Description:** Provide a clear description of the changes, link to the relevant issue(s), and explain the rationale.
10. **Review:** Address any feedback from maintainers during the code review process. Ensure CI checks pass on your PR.
11. **Merge:** Once approved and CI passes, a maintainer will merge your PR.

## Code Style

- Follow [Effective Go](https://go.dev/doc/effective_go) guidelines.
- Use `gofmt` and `goimports` for formatting (enforced by `make format`).
- Adhere to the linting rules defined in `.golangci.yml` (enforced by `make lint`).
- Write clear Go documentation comments for exported functions, types, and variables.
- Handle errors appropriately using Go's error handling patterns (e.g., checking `if err != nil`, using `fmt.Errorf` with `%w` for wrapping).

## Code of Conduct

This project adheres to the Contributor Covenant code of conduct. By participating, you are expected to uphold this code. Please report unacceptable behavior by reviewing the [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md).

Thank you for contributing to `convi`!
