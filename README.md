# docker-source-checksum

Calculates deterministic checksum of a Dockerfile from source.

The checksum is calculated from:

- Content of the dockerfile
- Content of local paths added in the dockerfile, from:
  - `ADD` command
  - `COPY` command that copies from local directory
  - `RUN` command that uses `--mount=type=bind`
- Parameters from `docker build`:
  - `--build-arg`
  - `--platform`
  - `--label`

Local source paths are normalized before globbing: `frontend`, `frontend/`,
`./frontend/`, and `/frontend/` select the same source content. Wildcards remain
supported. Dockerfile content is also hashed, so changing the spelling in the
Dockerfile still changes the overall checksum.

Unmatched sources emit a warning but remain allowed, including generated sources
that do not exist yet. Their content contributes nothing until it exists in the
build context. Generate required sources before calculating the checksum for a
complete source checksum. Remote `ADD` content is not fetched or hashed and may
also trigger an unmatched-source warning.

`.dockerignore` filtering is not supported: ignored local files are still hashed.
`COPY --from` sources are excluded because they do not come from the local context.

Normalizing previously omitted paths changes their checksums. Existing valid
paths retain their source contribution. Malformed glob patterns return an error.

Inspired by [github.com/dpc/docker-source-checksum](https://github.com/dpc/docker-source-checksum). The major difference is that this tool supports directory from `RUN --mount=type=bind` and handles variable expansion in dockerfile.

## Install

> TODO: add binary release

```sh
go install github.com/inoc603/dockerfile-source-checksum@latest
```

## Usage

```sh
dockerfile-source-checksum -f Dockerfile .
```

```sh
dockerfile-source-checksum \
    -f Dockerfile \
    --build-arg key1=value1 \
    --build-arg key2=value2 \
    --platform linux/amd64 \
    --platform linux/arm64/v8 \
    --label labe1=labelvalue1 \
    .
```
