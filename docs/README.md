# Llumnix documents

## Build the docs

- Make sure in `docs` directory

```bash
cd docs
```

- Install the dependencies:

```bash
pip install -r ./requirements_docs.txt
```

- Clean the previous build (optional but recommended):

```bash
make clean
```

- Generate the HTML documentation:

```bash
make html
```

Be sure to set `locale` right before building.

## Open the docs with your browser

- Serve the documentation locally:

```bash
python -m http.server -d build/html/
```

This will start a local server at http://localhost:8000. You can now open your browser and view the documentation.

If port 8000 is already in use, you can specify a different port, for example:

```bash
python -m http.server 3000 -d build/html/
```


## Add New Content

### Step 1: Create a new Markdown file

Create your new `.md` file under the appropriate directory:

```
docs/
├── getting_started/    # Getting started guides
├── design/             # Design documents
└── develop/            # Development guides
```

For example, to add a new design document:

```bash
touch docs/design/my_new_feature.md
```

### Step 2: Add an entry to index.md

Add a link to your new page in the corresponding section of `docs/index.md`:

```markdown
## 🛠️ Development Guide

- [My New Feature](design/my_new_feature.md) — Brief description of the new feature
```

### Step 3: Register in toctree

Add your new page to the corresponding `toctree` block at the bottom of `docs/index.md`:

```markdown
:::{toctree}
:hidden:
:caption: Design Documents

design/architecture
design/my_new_feature     # <-- add here, without .md extension
:::
```

> Note: Pages not listed in `toctree` will not appear in the sidebar navigation.

### Step 4: Rebuild and verify

```bash
make clean && make html
python -m http.server -d build/html/
```

Open http://localhost:8000 and verify your new page appears correctly.