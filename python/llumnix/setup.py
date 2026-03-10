import os
from typing import List
import subprocess

from setuptools import setup, find_packages
from setuptools.command.build_py import build_py
from setuptools_scm import get_version

ROOT_DIR = os.path.dirname(__file__)


class BuildPyCommand(build_py):
    """Custom build command to generate protobuf files before building."""

    def run(self):
        # Run make proto command
        subprocess.check_call(["make", "proto"])
        # Run the standard build process
        super().run()


def get_llumnix_version() -> str:
    git_describe_command = [
        "git",
        "describe",
        "--dirty",
        "--tags",
        "--long",
        "--match",
        "v*[0-9]*[0-9]*[0-9]",
    ]
    version = get_version(
        root="../..",
        write_to="python/llumnix/llumnix/version.py",
        git_describe_command=git_describe_command,
    )
    return version


def get_path(*filepath) -> str:
    return os.path.join(ROOT_DIR, "requirements", *filepath)


def get_llumnix_requirements() -> List[str]:
    """Get Python package dependencies from requirements.txt."""
    with open(get_path("common.txt"), encoding="utf-8") as f:
        requirements = f.read().strip().split("\n")
    return requirements


def get_engine_requirements(engine: str) -> List[str]:
    """Get Python package dependencies from requirements.txt."""
    with open(get_path(f"{engine}.txt"), encoding="utf-8") as f:
        requirements = f.read().strip().split("\n")
    return requirements


setup(
    name="llumnix",
    setup_requires=["setuptools_scm"],
    version=get_llumnix_version(),
    packages=find_packages(),
    install_requires=get_llumnix_requirements(),
    extras_require={
        "vllm": get_engine_requirements("vllm"),
        "sglang": get_engine_requirements("sglang"),
    },
    cmdclass={
        "build_py": BuildPyCommand,
    },
)
