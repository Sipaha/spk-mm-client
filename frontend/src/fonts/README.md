# Open Sans

Unmodified variable TrueType fonts from https://github.com/googlefonts/opensans
Revision: `bd7e37632246368c60fdcbd374dbf9bad11969b6`. Original files: `fonts/variable/OpenSans[wdth,wght].ttf`
and `fonts/variable/OpenSans-Italic[wdth,wght].ttf`.
License: SIL OFL 1.1, shipped as `../../public/licenses/OpenSans-LICENSE.txt`.

Bundled locally, including Cyrillic, normal width, upright and italic weights.
Open Sans replaces Source Sans 3 after the user's comparison with the official
Mattermost client: Source Sans looked horizontally compressed.

On the dark theme, `-webkit-font-smoothing: antialiased` selects grayscale
antialiasing in WebKit. Compared in system WebKitGTK on 2026-10-02 after the
user requested softer text: removes the coloured edges of default RGB
subpixel rendering, preserving the font family, size and colours.
This is an app stylesheet preference; OS font settings are not modified.

The user subsequently requested fuller strokes. The normal weight is 450
(previously 400), inherited from `html` and shared with Tailwind's `font-normal`.
Medium 500, semibold 600 and bold 700 remain distinct; upright and italic
variable fonts both support the intermediate weight.
