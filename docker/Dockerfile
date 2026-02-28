ARG ARCH=amd64
ARG TAG_LLM_GATEWAY=0.0.1
FROM registry.cn-shanghai.aliyuncs.com/eas/golang-${ARCH}-cache:1.23.12 AS builder

COPY .ssh /root/.ssh
COPY .gitconfig /root/.gitconfig
COPY LLM-Gateway /LLM-Gateway

WORKDIR /LLM-Gateway

ARG ARCH
RUN mkdir -p /output && \
    OUTPUT=/output GOARCH=${ARCH} sh /LLM-Gateway/docker/build.sh

FROM registry.cn-shanghai.aliyuncs.com/eas/debian:${ARCH}-bookworm-slim-base

# download built-in tokenizers
ADD https://pai-quickstart-cn-shanghai.oss-cn-shanghai.aliyuncs.com/modelscope/deepseek/DeepSeek-R1-0528/tokenizer.json /tokenizers/DeepSeek-R1-0528/tokenizer.json
ADD https://pai-quickstart-cn-shanghai.oss-cn-shanghai.aliyuncs.com/modelscope/deepseek/DeepSeek-R1-0528/tokenizer_config.json /tokenizers/DeepSeek-R1-0528/tokenizer_config.json

# add llm-gateway binary
COPY --from=builder /output/llm-gateway /

WORKDIR /
CMD ["bash"]
