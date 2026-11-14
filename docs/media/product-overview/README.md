# Product overview: Chinese and English

The two narrated product overviews use the same 27-scene sequence and four
shared illustrations. Text and narration are localized for each language;
their durations can differ. The animations keep the same layout and motion
while their on-screen labels are translated. This is an illustrated overview,
not a recording of the product UI.

| Source | Purpose |
| --- | --- |
| `explainer.zh.xml` | Chinese storyboard, animation source and narration |
| `explainer.en.xml` | Corresponding English storyboard and narration |
| `narration.zh.txt`, `transcript.en.md` | Readable narration transcripts |
| `images/` | Shared, text-free illustrations for both versions |
| `build_english.py` | Regenerates the English storyboard from the Chinese source |

Run `python3 build_english.py` in this directory after editing the source or
the translation mapping. The script checks scene order, layouts, image prompts,
anchors and the language of the English narration. Review both storyboards
against current product behavior before publishing an update.

When rendering a new video with Explain Video Generator, upload each file in
`images/` to each video and replace the matching image-prompt item with the
attachment ID returned for that particular video. The mapping is:

| Scene anchor | Shared image |
| --- | --- |
| `connected` | `images/scene-connected.png` |
| `deployment` | `images/scene-deployment.png` |
| `security-hero` | `images/scene-security.png` |
| `reuse` | `images/scene-reuse.png` |

Do not reuse attachment IDs across videos. Verify the complete playback,
captions, language, scene order and public viewing links before updating either
project README. Do not commit account-claim links or credentials.
