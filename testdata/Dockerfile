ARG ARG1

FROM alpine as builder

RUN --mount=type=bind,source=./dist,target=/app/dist \
	--mount=type=cache,target=/go/pkg/mod \
	ls . | xarg echo

###############################################################################

FROM alpine

WORKDIR /app

ENV ENV1=d

RUN --mount=type=bind,from=builder,source=/etc/tmp,target=/app/dist \
	echo 1

COPY ./a/* /app
COPY ./${ARG1} /app
ADD ./c /app
COPY ./${ENV1} /app
COPY --from=builder ./a /app
