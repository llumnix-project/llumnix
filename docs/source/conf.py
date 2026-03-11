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
    "repository_url": "https://github.com/llumnix-project/llumnix",
    "use_repository_button": True,
    "home_page_in_toc": True,
    "collapse_navbar": False,
    "show_navbar_depth": 2,
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
