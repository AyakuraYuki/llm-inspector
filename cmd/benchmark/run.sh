#!/bin/bash

# API Key
export OPENAI_API_KEY=""

# Base URL（Optional, default to OpenAI API Base URL）
#export OPENAI_BASE_URL=""

# Model name（Optional, default to gpt-4）
#export MODEL_NAME=""

# Run benchmark
cd "$(dirname "$0")" || exit
./benchmark
