SHELL := /bin/sh
.DEFAULT_GOAL := help

PLUGIN_MANIFEST := herdr-plugin.toml
GIT_CLIFF := git-cliff

.PHONY: help next-version release

help: ## Show available commands.
	@awk 'BEGIN { FS = ":.*##"; printf "Usage:\n  make <command>\n\nCommands:\n" } /^[a-zA-Z0-9_-]+:.*##/ { printf "  %-20s %s\n", $$1, $$2 }' $(MAKEFILE_LIST)

next-version: ## Show the next SemVer inferred from Conventional Commits.
	@last_tag="$$(git describe --tags --match 'v[0-9]*' --abbrev=0 2>/dev/null || true)"; \
	if [ -z "$$last_tag" ]; then \
		printf '%s\n' 'No v* release tag found. Create the baseline tag first:'; \
		printf '%s\n' '  git tag -a v0.1.0 -m v0.1.0'; \
		printf '%s\n' '  git push origin v0.1.0'; \
		exit 1; \
	fi; \
	range="$$last_tag..HEAD"; \
	if ! git log --format=%s "$$range" | grep -Eq '^(feat|fix|perf|refactor|docs)(\([^)]+\))?!?: .+|^[a-z]+(\([^)]+\))?!: .+' && \
		! git log --format=%B "$$range" | grep -Eq '^BREAKING[ -]CHANGE: .+'; then \
		printf 'No release bump commits found since %s. ci:, test:, and chore: do not trigger releases.\n' "$$last_tag"; \
		exit 1; \
	fi; \
	current_version="$$(sed -nE 's/^[[:space:]]*version[[:space:]]*=[[:space:]]*"([^"]+)".*/\1/p' $(PLUGIN_MANIFEST) | head -n 1)"; \
	new_version="$$($(GIT_CLIFF) --bumped-version)"; \
	new_version="$${new_version#v}"; \
	if [ "$$new_version" = "$$current_version" ]; then \
		printf 'No release bump: manifest version remains %s\n' "$$current_version"; \
		exit 1; \
	fi; \
	printf '%s\n' "$$new_version"

release: ## Bump the manifest, commit, tag, and push a release.
	@test "$$(git branch --show-current)" = 'main' || { printf 'Release must run from main, not %s.\n' "$$(git branch --show-current)"; exit 1; }; \
	test -z "$$(git status --porcelain)" || { printf '%s\n' 'Working tree must be clean before release.'; git status --short; exit 1; }; \
	git fetch --tags origin; \
	last_tag="$$(git describe --tags --match 'v[0-9]*' --abbrev=0 2>/dev/null || true)"; \
	if [ -z "$$last_tag" ]; then \
		printf '%s\n' 'No v* release tag found. Create the baseline tag first:'; \
		printf '%s\n' '  git tag -a v0.1.0 -m v0.1.0'; \
		printf '%s\n' '  git push origin v0.1.0'; \
		exit 1; \
	fi; \
	range="$$last_tag..HEAD"; \
	if ! git log --format=%s "$$range" | grep -Eq '^(feat|fix|perf|refactor|docs)(\([^)]+\))?!?: .+|^[a-z]+(\([^)]+\))?!: .+' && \
		! git log --format=%B "$$range" | grep -Eq '^BREAKING[ -]CHANGE: .+'; then \
		printf 'No release bump commits found since %s. ci:, test:, and chore: do not trigger releases.\n' "$$last_tag"; \
		exit 1; \
	fi; \
	current_version="$$(sed -nE 's/^[[:space:]]*version[[:space:]]*=[[:space:]]*"([^"]+)".*/\1/p' $(PLUGIN_MANIFEST) | head -n 1)"; \
	new_version="$$($(GIT_CLIFF) --bumped-version)"; \
	new_version="$${new_version#v}"; \
	if [ "$$new_version" = "$$current_version" ]; then \
		printf 'No release bump: manifest version remains %s\n' "$$current_version"; \
		exit 1; \
	fi; \
	tag="v$$new_version"; \
	! git rev-parse "$$tag" >/dev/null 2>&1 || { printf 'Tag already exists: %s\n' "$$tag"; exit 1; }; \
	VERSION="$$new_version" perl -0pi -e 's/(^version\s*=\s*")[^"]+(")/$$1$$ENV{VERSION}$$2/m' $(PLUGIN_MANIFEST); \
	git add $(PLUGIN_MANIFEST); \
	git commit -m "chore: release $$tag"; \
	git tag -a "$$tag" -m "$$tag"; \
	git push origin HEAD; \
	git push origin "$$tag"
