package i18n

// englishTranslations contains all English translations
var englishTranslations = map[string]string{
	// Status messages
	"status.ready":                     "Ready to receive messages",
	"status.post_request":              "Ctrl+E open input editor · Ctrl+L clear messages · Ctrl+O insert in buffer · Ctrl+Y copy to clipboard · Enter send another query.",
	"status.source_opened":             "Source opened. Use up/down arrows to navigate and type questions in the sidebar.",
	"status.response_added":            "Response added to sidebar.",
	"status.no_sources":                "No sources available to navigate.",
	"status.source_mode_active":        "Source mode active. Press 1-9 to open a source or Ctrl+C to go back.",
	"status.source_search_prompt":      "Search in source: type a term and press Enter.",
	"status.source_search_empty":       "Search cleared.",
	"status.source_search_results":     "%d results found.",
	"status.source_search_title":       "Search in source",
	"status.source_search_placeholder": "Words to search...",
	"status.semantic_cache_continue":   "Semantic cache miss; searching the web...",
	"status.searching_sources":         "Searching for sources on the web...",
	"status.downloading_sources":       "Downloading sources...",
	"status.processing_sources":        "Processing sources...",
	"status.request_canceled":          "Request canceled",
	"status.write_message":             "Write a message or a slash command.",
	"status.no_response_accept":        "No response to accept yet.",
	"status.mode_activated":            "Mode %s activated.",
	"status.no_response_copy":          "No response to copy yet.",
	"status.copy_failed":               "Could not copy response to clipboard.",
	"status.response_copied":           "Response copied to clipboard.",
	"status.editor_failed":             "Could not initialize external editor.",
	"status.cache_updated":             "Semantic cache updated in Qdrant.",
	"status.saving_cache":              "Saving results in semantic cache...",
	"status.cache_update_failed":       "Could not update semantic cache.",
	"status.reusing_cache":             "Reusing semantic cache...",

	// Help text - Source selection state
	"help.source_select": "1-9 open source · Ctrl+C go back · Arrows navigate content · Enter ask about the current source",

	// Help text - Source view state
	"help.source_view": "Ctrl+F search in source · Ctrl+N next match · Ctrl+Shift+N previous match · Enter ask about the source · Arrows up/down navigate Markdown · Shift+Up/Down scroll sidebar · Ctrl+S change source · Ctrl+C go back",

	// Help text - Main shortcuts
	"help.shortcuts": "Enter send · Tab autocomplete · Ctrl+S sources · Ctrl+T mode · Ctrl+K reasoning · Ctrl+E edit input · Ctrl+L clear · Ctrl+O accept · Ctrl+Y copy · Ctrl+C cancel/exit · Esc exit",

	// Help text - Slash commands
	"help.no_slash_commands":     "no slash commands",
	"help.modal.title":           "Help",
	"help.modal.esc":             "esc close",
	"help.modal.shortcuts":       "Shortcuts",
	"help.modal.slash":           "Slash",
	"help.slash.help":            "Show shortcuts and slash commands",
	"help.slash.no_description":  "No description",
	"help.shortcut.enter":        "Send",
	"help.shortcut.tab":          "Autocomplete",
	"help.shortcut.ctrl_p":       "Open help",
	"help.shortcut.ctrl_s":       "Open source selector",
	"help.shortcut.ctrl_f":       "Search in current source",
	"help.shortcut.ctrl_n":       "Next source match",
	"help.shortcut.ctrl_shift_n": "Previous source match",
	"help.shortcut.ctrl_t":       "Switch mode",
	"help.shortcut.ctrl_k":       "Toggle reasoning view",
	"help.shortcut.ctrl_e":       "Open external editor",
	"help.shortcut.ctrl_l":       "Clear conversation",
	"help.shortcut.ctrl_o":       "Accept latest response",
	"help.shortcut.ctrl_y":       "Copy latest response",
	"help.shortcut.ctrl_c":       "Cancel request or exit",
	"help.shortcut.esc":          "Close help or exit",
	"help.shortcut.up_down":      "Scroll help modal",

	// Content pane messages
	"pane.source_questions_title": "## Questions about the source\n\nType a question in the input below and the answer will appear here without hiding the Markdown on the left.\n\nCtrl+C goes back to the conversation.",

	"pane.sidebar_title": "## Source Sidebar\n\nPress Ctrl+S and then 1-9 to open a source from the last /search.\n\nWhen you open a source, questions and answers will appear here.",

	"pane.no_sidebar_hint": "## Sidebar\n\nRun /search to enable source navigation and ask contextual questions about a downloaded URL.",

	"pane.no_sources_available": "## No sources available\n\nRun /search to populate the list of navigable sources.",

	"pane.source_selection": "## Source Selection\n\nPress the number of a source to download it and open it in navigation mode.\n\n",

	"pane.downloading_source": "## Downloading source %d\n\nWait while the selected URL is downloaded and converted to readable content.",

	"pane.downloading_source_generic": "## Downloading source\n\nWait while the selected URL is downloaded and converted to readable content.",

	// Progress steps and messages
	"progress.rewrite_query":        "Optimizing query",
	"progress.prepare_context":      "Preparing context",
	"progress.process_response":     "Processing response",
	"progress.searching_sources":    "Searching sources",
	"progress.downloading_sources":  "Downloading sources",
	"progress.processing_sources":   "Processing sources",
	"progress.checking_cache":       "Checking semantic cache",
	"progress.ranking_sources":      "Ranking relevance",
	"progress.saving_cache":         "Saving semantic cache",
	"progress.token_usage":          "Token usage",
	"progress.context_exceeds":      "Context exceeds %d tokens. Summarizing sources individually",
	"progress.summaries_ready":      "Per-source summaries ready for final summary",
	"progress.llm_elapsed":          "LLM elapsed time: %ds",
	"progress.llm_total":            "Total LLM time: %ds",
	"progress.llm_summarizing":      "Querying LLM to summarize the information",
	"progress.llm_summary_received": "LLM summary received",
	"progress.summarizing_source":   "Summarizing source %d/%d: %s",
	"progress.source_summarized":    "Source %d/%d summarized: %s",
	"progress.total_time":           "Total time (%s)",
	"progress.candidates":           "Candidates (%d):",

	// Slash command mode indicator
	"mode.indicator":      " mode · Ctrl+T",
	"mode.help_indicator": "Ctrl+P · Show help",
	"mode.normal":         "Normal",
	"mode.reasoning":      "Reasoning",
	"mode.chat":           "Chat",

	// Sources section
	"section.sources": "Sources:\n",

	// Additional status messages
	"status.consulting_cache":       "Consulting semantic cache...",
	"status.generating_response":    "Generating response...",
	"status.updating_progress":      "Updating progress...",
	"status.messages_cleared":       "Messages deleted.",
	"status.source_not_found":       "Selected source does not exist.",
	"status.downloading_source":     "Downloading and cleaning selected source...",
	"status.llm_on_source":          "Querying LLM about the opened source...",
	"status.preparing_web_search":   "Preparing web search...",
	"status.querying_ollama":        "Querying Ollama",
	"status.receiving_llm_response": "Receiving LLM response",
	"status.request_failed_retry":   "An error occurred. You can retry.",
	"status.editor_updated":         "Input updated from the editor.",
	"status.editor_updated_from":    "Input updated from %s.",
	"status.config_opening":         "Opening configuration in the editor...",
	"status.config_reloaded":        "Configuration reloaded successfully.",
	"status.config_reloaded_from":   "Configuration reloaded from %s.",
	"status.reasoning_expanded":     "Reasoning expanded.",
	"status.reasoning_collapsed":    "Reasoning collapsed.",

	// Reasoning placeholder
	"reasoning.placeholder": "Reasoning...",

	// Error messages
	"error.timeout_web_search": "Timeout during web search",
	"error.timeout_llm":        "Timeout waiting for LLM response",
	"error.web_search":         "Web search error: %s",
	"error.llm":                "LLM error: %s",
	"error.timeout_response":   "Timeout waiting for response",

	// Source question prompt text
	"prompt.source_only_answer":    "Answer using only the provided source. If the answer is not in the content, say so clearly.",
	"prompt.source_label":          "Source",
	"prompt.source_title_label":    "Title",
	"prompt.source_markdown_label": "Clean content in Markdown",
	"prompt.user_question_label":   "User question",
	"prompt.answer_same_language":  "Answer in the dominant language of the question. Use Markdown when it helps readability.",

	// External editor errors
	"editor.prepare_temp_failed": "Could not prepare the temporary file for %s: %v",
	"editor.write_temp_failed":   "Could not write content for %s: %v",
	"editor.close_temp_failed":   "Could not close the temporary file for %s: %v",
	"editor.open_failed":         "Could not open %s: %v",
	"editor.read_updated_failed": "Could not read edited content from %s: %v",
}
