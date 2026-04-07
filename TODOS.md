# TODOs

## Audio Transcripts

- `transcript:` / `endtranscript` ya delimita transcripciones de audio en la sesión.
- Añadir una instrucción en `CLAUDE.md`, `AGENTS.md` o el equivalente del agente para tratar estos bloques como STT potencialmente imperfecto.
- Pedir especial cautela con números, SKUs, teléfonos, placas, códigos y nombres raros dentro de bloques `transcript`.
- Considerar heurísticas de normalización para secuencias numéricas ambiguas como `8.22.49`, `8 22 49` o `8-22-49` cuando el contexto sugiera un identificador compacto como `82249`.
- Evaluar una etapa opcional de confirmación o edición antes de enviar la transcripción al agente.
