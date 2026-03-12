// Remove tex2jax_ignore class from sections containing math
// This ensures MathJax processes math formulas even in sections with mermaid diagrams
document.addEventListener("DOMContentLoaded", function() {
    // Find all sections with math formulas
    const sections = document.querySelectorAll('section.tex2jax_ignore, section.mathjax_ignore');
    sections.forEach(function(section) {
        // Check if section contains math elements
        const hasMath = section.querySelector('.math, .mathjax_process, .tex2jax_process, .mermaid');
        if (hasMath) {
            // Remove the ignore classes so MathJax can process the section
            section.classList.remove('tex2jax_ignore', 'mathjax_ignore');
            // Add processing class
            section.classList.add('tex2jax_process');
        }
    });

    // If MathJax is already loaded, re-typeset the page
    if (typeof MathJax !== 'undefined' && MathJax.typesetPromise) {
        MathJax.typesetPromise();
    }
});
