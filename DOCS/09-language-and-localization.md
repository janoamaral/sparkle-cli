# Language and localization

sparkle-cli localizes UI text automatically.

## Detection

Language is detected from your system `LANG` environment variable.

- If it starts with `es`, UI is shown in Spanish.
- Otherwise, UI defaults to English.

## What is localized

- Status messages
- Help footer text
- Source mode guidance
- Progress labels
- Error messages

## Search response language

For `/search`, the model is instructed to answer in the same dominant language as your original query.

This means Spanish questions should return Spanish answers, and English questions should return English answers (unless explicitly requested otherwise).
