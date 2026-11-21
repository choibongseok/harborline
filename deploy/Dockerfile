# 릴리스 이미지. 정적 바이너리 하나만 담는다(셸·패키지 관리자·외부 호출 도구 없음).
FROM scratch
ARG VERSION
ARG FEATURES
ARG BREAK
COPY harborline /harborline
ENV HARBORLINE_VERSION=${VERSION} HARBORLINE_FEATURES=${FEATURES} HARBORLINE_BREAK=${BREAK}
USER 10001:10001
ENTRYPOINT ["/harborline"]
CMD ["serve"]
