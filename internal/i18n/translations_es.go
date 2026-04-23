package i18n

// spanishTranslations contains all Spanish translations
var spanishTranslations = map[string]string{
	// Status messages
	"status.ready":                   "Listo para recibir mensajes",
	"status.post_request":            "Ctrl+E abre editor del input · Ctrl+L limpia mensajes · Ctrl+O inserta en buffer · Ctrl+Y copia al clipboard · Enter envia otra consulta.",
	"status.source_opened":           "Fuente abierta. Usa flechas arriba/abajo para navegar y escribe preguntas en el sidebar.",
	"status.response_added":          "Respuesta agregada al sidebar.",
	"status.no_sources":              "No hay fuentes disponibles para navegar.",
	"status.source_mode_active":      "Modo fuentes activo. Presiona 1-9 para abrir una fuente o Ctrl+C para volver.",
	"status.semantic_cache_continue": "Cache semantica sin hits; buscando en la web...",
	"status.searching_sources":       "Buscando fuentes en la web...",
	"status.downloading_sources":     "Descargando fuentes...",
	"status.processing_sources":      "Procesando fuentes...",
	"status.request_canceled":        "Peticion cancelada",
	"status.write_message":           "Escribe un mensaje o un slash command.",
	"status.no_response_accept":      "No hay respuesta para aceptar todavia.",
	"status.mode_activated":          "Modo %s activado.",
	"status.no_response_copy":        "No hay respuesta para copiar todavia.",
	"status.copy_failed":             "No se pudo copiar la respuesta al clipboard.",
	"status.response_copied":         "Respuesta copiada al clipboard.",
	"status.editor_failed":           "No se pudo inicializar el editor externo.",
	"status.cache_updated":           "Cache semantica actualizada en Qdrant.",
	"status.saving_cache":            "Guardando resultados en cache semantica...",
	"status.cache_update_failed":     "No se pudo actualizar la cache semantica.",
	"status.reusing_cache":           "Reutilizando cache semantica...",

	// Help text - Source selection state
	"help.source_select": "1-9 abrir fuente · Ctrl+C volver · Flechas navegar contenido · Enter pregunta sobre la fuente actual",

	// Help text - Source view state
	"help.source_view": "Enter pregunta sobre la fuente · Flechas arriba/abajo navegan el Markdown · Shift+Up/Down scroll sidebar · Ctrl+S cambia de fuente · Ctrl+C vuelve",

	// Help text - Main shortcuts
	"help.shortcuts": "Enter enviar · Tab autocompleta · Ctrl+S fuentes · Ctrl+T modo · Ctrl+E editar input · Ctrl+L limpiar · Ctrl+O aceptar · Ctrl+Y copiar · Ctrl+C cancelar/salir · Esc salir",

	// Help text - Slash commands
	"help.no_slash_commands":    "sin slash commands",
	"help.slash_commands_count": "%d slash commands · / autocompleta",

	// Content pane messages
	"pane.source_questions_title": "## Preguntas sobre la fuente\n\nEscribe una pregunta en el input inferior y la respuesta aparecera aqui sin ocultar el Markdown de la izquierda.\n\nCtrl+C vuelve a la conversacion.",

	"pane.sidebar_title": "## Sidebar de fuentes\n\nPresiona Ctrl+S y luego 1-9 para abrir una fuente del ultimo /search.\n\nCuando abras una fuente, las preguntas y respuestas apareceran aqui.",

	"pane.no_sidebar_hint": "## Sidebar\n\nEjecuta /search para habilitar la navegacion por fuentes y hacer preguntas contextuales sobre una URL descargada.",

	"pane.no_sources_available": "## Sin fuentes disponibles\n\nEjecuta /search para poblar la lista de fuentes navegables.",

	"pane.source_selection": "## Seleccion de fuentes\n\nPresiona el numero de una fuente para descargarla y abrirla en modo navegacion.\n\n",

	"pane.downloading_source": "## Descargando fuente %d\n\nEspera mientras se descarga la URL seleccionada y se convierte a contenido legible.",

	"pane.downloading_source_generic": "## Descargando fuente\n\nEspera mientras se descarga la URL seleccionada y se convierte a contenido legible.",

	// Progress steps and messages
	"progress.rewrite_query":        "Optimizando query",
	"progress.prepare_context":      "Preparando contexto",
	"progress.process_response":     "Procesando respuesta",
	"progress.searching_sources":    "Buscando fuentes",
	"progress.downloading_sources":  "Descargando fuentes",
	"progress.processing_sources":   "Procesando fuentes",
	"progress.checking_cache":       "Consultando cache semantica",
	"progress.ranking_sources":      "Procesando relevancia",
	"progress.saving_cache":         "Guardando cache semantica",
	"progress.token_usage":          "Token usage",
	"progress.context_exceeds":      "El contexto supera %d tokens. Resumiendo fuentes individualmente",
	"progress.summaries_ready":      "Resúmenes por fuente listos para el resumen final",
	"progress.llm_elapsed":          "Tiempo transcurrido del LLM: %ds",
	"progress.llm_total":            "Tiempo total del LLM: %ds",
	"progress.llm_summarizing":      "Consultando LLM para resumir la informacion",
	"progress.llm_summary_received": "Resumen del LLM recibido",
	"progress.summarizing_source":   "Resumiendo fuente %d/%d: %s",
	"progress.source_summarized":    "Fuente %d/%d resumida: %s",
	"progress.total_time":           "Tiempo total (%s)",
	"progress.candidates":           "Candidatos (%d):",

	// Slash command mode indicator
	"mode.indicator": " modo · Ctrl+T",
	"mode.normal":    "Normal",
	"mode.reasoning": "Razonamiento",
	"mode.chat":      "Chat",

	// Sources section
	"section.sources": "Fuentes:\n",

	// Additional status messages
	"status.consulting_cache":       "Consultando cache semantica...",
	"status.generating_response":    "Generando respuesta...",
	"status.updating_progress":      "Actualizando progreso...",
	"status.messages_cleared":       "Mensajes eliminados.",
	"status.source_not_found":       "La fuente seleccionada no existe.",
	"status.downloading_source":     "Descargando y limpiando la fuente seleccionada...",
	"status.llm_on_source":          "Consultando el LLM sobre la fuente abierta...",
	"status.preparing_web_search":   "Preparando busqueda web...",
	"status.querying_ollama":        "Consultando Ollama",
	"status.receiving_llm_response": "Recibiendo respuesta del LLM",
	"status.request_failed_retry":   "Ocurrio un error. Puedes reintentar.",
	"status.editor_updated":         "Input actualizado desde el editor.",
	"status.editor_updated_from":    "Input actualizado desde %s.",

	// Error messages
	"error.timeout_web_search": "Timeout durante la busqueda web",
	"error.timeout_llm":        "Timeout esperando respuesta del LLM",
	"error.web_search":         "Error durante la busqueda web: %s",
	"error.llm":                "Error del LLM: %s",
	"error.timeout_response":   "Timeout esperando respuesta",

	// Source question prompt text
	"prompt.source_only_answer":    "Responde usando exclusivamente la fuente proporcionada. Si la respuesta no esta en el contenido, dilo con claridad.",
	"prompt.source_label":          "Fuente",
	"prompt.source_title_label":    "Titulo",
	"prompt.source_markdown_label": "Contenido limpio en Markdown",
	"prompt.user_question_label":   "Pregunta del usuario",
	"prompt.answer_same_language":  "Responde en el mismo idioma dominante de la pregunta. Usa Markdown cuando ayude a la legibilidad.",

	// External editor errors
	"editor.prepare_temp_failed": "No se pudo preparar el archivo temporal para %s: %v",
	"editor.write_temp_failed":   "No se pudo escribir el contenido para %s: %v",
	"editor.close_temp_failed":   "No se pudo cerrar el archivo temporal para %s: %v",
	"editor.open_failed":         "No se pudo abrir %s: %v",
	"editor.read_updated_failed": "No se pudo leer el contenido editado desde %s: %v",
}
