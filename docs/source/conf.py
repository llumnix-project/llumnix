import datetime
import os
import sys
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parent.parent.parent
sys.path.append(os.path.abspath(REPO_ROOT))

project = 'Llumnix'
copyright = f'{datetime.datetime.now().year}, AlibabaPAI Team'
author = 'AlibabaPAI'
mermaid_output_format = "raw"

extensions = [
    "sphinx.ext.napoleon",    
    "sphinx.ext.intersphinx", 
    "sphinx_copybutton",                 
    "myst_parser",            
    "sphinx_design",          
    "sphinx_togglebutton",    
    "sphinxcontrib.mermaid",
]

myst_enable_extensions = [
    "colon_fence",
    "fieldlist",
    "html_admonition",
    "attrs_inline",
    "dollarmath",
    "amsmath",
]

myst_heading_anchors = 3

html_title = project
html_theme = 'sphinx_book_theme'

html_theme_options = {
    "repository_url": "https://github.com/your-username/llumnix",
    "use_repository_button": True,
    "use_edit_page_button": True,
    "path_to_docs": "docs/source",
    "home_page_in_toc": True,
    "collapse_navbar": True,
    "show_navbar_depth": 1,
    "default_mode": "light", 
}


autodoc_mock_imports = [
    "torch",
    "vllm",
    "numpy",
    "pydantic",
    "transformers",
    "redis",
    "readerwriterlock",
]

html_static_path = ["_static"]
html_css_files = ["custom.css"]

myst_url_schemes = {
    "gh-file": {
        "url": "https://github.com/your-username/llumnix/blob/main/{{path}}",
        "title": "{{path}}",
    },
}
