#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
MODELS="${1:-$ROOT/models}"
BASE_URL="https://github.com/k2-fsa/sherpa-onnx/releases/download/asr-models"
SPEAKER_URL="https://github.com/k2-fsa/sherpa-onnx/releases/download/speaker-recongition-models"
HF_MIRROR="https://hf-mirror.com/csukuangfj/speaker-embedding-models/resolve/main"
ASR_DIR="sherpa-onnx-streaming-zipformer-zh-int8-2025-06-30"
SECONDARY_ASR_DIR="sherpa-onnx-streaming-zipformer-zh-14M-2023-02-23"
REFINE_DIR="sherpa-onnx-sense-voice-zh-en-ja-ko-yue-int8-2024-07-17"
SPEAKER_MODEL="3dspeaker_speech_campplus_sv_zh-cn_16k-common.onnx"

speaker_model_name() {
  case "$1" in
    campplus) printf '%s\n' "3dspeaker_speech_campplus_sv_zh-cn_16k-common.onnx" ;;
    eres2net) printf '%s\n' "3dspeaker_speech_eres2net_sv_zh-cn_16k-common.onnx" ;;
    eres2net-base) printf '%s\n' "3dspeaker_speech_eres2net_base_200k_sv_zh-cn_16k-common.onnx" ;;
    eres2net-large) printf '%s\n' "3dspeaker_speech_eres2net_large_sv_zh-cn_3dspeaker_16k.onnx" ;;
    wespeaker-r34) printf '%s\n' "wespeaker_cnceleb_resnet34.onnx" ;;
    *) return 1 ;;
  esac
}

EXTRA_SPEAKER=""
for arg in "$@"; do
  case "$arg" in
    --speaker-model=*)
      key="${arg#--speaker-model=}"
      if ! EXTRA_SPEAKER="$(speaker_model_name "$key")"; then
        echo "未知模型: $key (可选: campplus eres2net eres2net-base eres2net-large wespeaker-r34)" >&2
        exit 1
      fi
      ;;
  esac
done

mkdir -p "$MODELS"
cd "$MODELS"

if [[ ! -f silero_vad.onnx ]]; then
  curl --fail --location --retry 3 --output silero_vad.onnx.download "$BASE_URL/silero_vad.onnx"
  mv silero_vad.onnx.download silero_vad.onnx
fi

download_speaker_model() {
  local model="$1"
  if [[ -f "$model" ]]; then
    return
  fi
  echo "下载说话人模型: $model"
  if ! curl --fail --location --retry 2 --retry-all-errors --connect-timeout 20 --output "$model.download" "$SPEAKER_URL/$model"; then
    echo "GitHub 直连失败，改用 hf-mirror" >&2
    curl --fail --location --retry 3 --retry-all-errors --connect-timeout 20 --output "$model.download" "$HF_MIRROR/$model"
  fi
  mv "$model.download" "$model"
}

download_speaker_model "$SPEAKER_MODEL"
if [[ -n "$EXTRA_SPEAKER" && "$EXTRA_SPEAKER" != "$SPEAKER_MODEL" ]]; then
  download_speaker_model "$EXTRA_SPEAKER"
fi

for MODEL_DIR in "$ASR_DIR" "$SECONDARY_ASR_DIR" "$REFINE_DIR"; do
  if [[ ! -d "$MODEL_DIR" ]]; then
    curl --fail --location --retry 3 --output "$MODEL_DIR.tar.bz2.download" "$BASE_URL/$MODEL_DIR.tar.bz2"
    mv "$MODEL_DIR.tar.bz2.download" "$MODEL_DIR.tar.bz2"
    tar -xjf "$MODEL_DIR.tar.bz2"
    rm "$MODEL_DIR.tar.bz2"
  fi
done

printf 'Models ready in %s\n' "$MODELS"
