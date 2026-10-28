import unicodedata


def sanitize_text(text: str) -> str:
    normalized = unicodedata.normalize("NFC", text)
    return "".join(
        character
        for character in normalized
        if character in "\n\t" or unicodedata.category(character) not in {"Cc", "Cf", "Cs"}
    ).strip()
