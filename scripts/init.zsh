function sparkle-cli-widget() {
  emulate -L zsh
  setopt local_options pipe_fail no_aliases

  local original_buffer="$BUFFER"
  local result_file
  result_file="$(mktemp "${TMPDIR:-/tmp}/sparkle-cli-result.XXXXXX")" || return 1

  zle -I
  sparkle-cli --context "$BUFFER" --result-file "$result_file"
  local exit_code=$?
  local output=""

  if [[ -s "$result_file" ]]; then
    output="$(<"$result_file")"
  fi
  rm -f "$result_file"

  if [[ $exit_code -eq 0 && -n "$output" ]]; then
    BUFFER="$output"
    CURSOR=${#BUFFER}
  else
    BUFFER="$original_buffer"
    CURSOR=${#BUFFER}
  fi

  zle redisplay
}

zle -N sparkle-cli-widget
bindkey '^G' sparkle-cli-widget