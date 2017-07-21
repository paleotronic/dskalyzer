# dskalyzer
DSKalyzer Apple II disk management tool

DSKalyzer is a command line tool for analyzing and managing Apple II DSK images and their archives. Its features include not only the standard set of disk manipulation tools -- extract (with text conversion), import to disk (including tokenisation of Applesoft BASIC), delete, and so forth -- but also the ability to identify duplicates — complete, active sector, and subset; find file, sector and other commonalities between disks (including as a percentage of similarity or difference); search de-tokenized BASIC, text and binary / sector data; generate reports identifying and / or collating disk type, DOS, geometry, size, and so forth; allowing for easier, semi-automated DSK archival management and research. 

DSKalyzer works by first “ingesting” your disk(s), creating an index containing various pieces of information (disk / sector / file hashes, catalogs, text data, binary data etc.) about each disk that is then searchable using the same tool. This way you can easily find information stored on disks without tediously searching manually or through time-consuming multiple image scans. You can also identify duplicates, quasi-duplicates (disks with only minor differences or extraneous data), or iterations, reducing redundancies.

Once you've identified a search you can also extract selected files. DSKalyzer can report to standard output (terminal), to a text file, or to a CSV file.

Supports DOS, ProDOS, RDOS and Pascal (READ); DOS, Prodos (WRITE); 140K-800K disks (DSK, PO, DO) and variable-sized 2MG files. Runs on MacOS X, Windows, FreeBSD and Linux; i386, amd64 and ARM. 

Run without flags for interactive shell (currently supports most common disk management tasks). Run with the -h or -help flag for list of flags (for advanced searching and comparison features). More documentation coming soon!

THIS IS ALPHA SOFTWARE. PLEASE ENSURE YOU HAVE A BACKUP OF ALL DISKS YOU SCAN / MODIFY WITH DSKALYZER!

Binaries are available in the Releases tab https://github.com/paleotronic/dskalyzer/releases

Website: http://octalyzer.com/dskalyzer
