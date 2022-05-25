# NPM Cleaner
Quick-and-dirty script to scan for `node_modules` folders above a 
given size limit and whose parent project has not had any file modifications within
a given number of days.

Running on it's own will list candidates, passing the `-delete` flag will remove the folders.

A bit Windows-specific, and limits and starting path are all hard-coded.