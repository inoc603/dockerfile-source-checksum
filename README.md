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

Inspired by [github.com/dpc/docker-source-checksum](https://github.com/dpc/docker-source-checksum). The major difference is that this tool supports directory from `RUN --mount=type=bind` and handles variable expansion in dockerfile.

## Install

> TODO: add binary release

```sh
go install github.com/inoc603/docker-source-checksum@latest
```

## Usage

```sh
docker-source-checksum -f Dockerfile .
```

```sh
docker-source-checksum \
    -f Dockerfile \
    --build-arg key1=value1 \
    --build-arg key2=value2 \
    --platform linux/amd64 \
    --platform linux/arm64/v8 \
    --label labe1=labelvalue1 \
    .
```
