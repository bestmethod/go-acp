##### 1.0
* first stable release

##### 1.1
* improvement: implement sleep locks on threads (so if reader is much faster than writer, it won't eat up the CPU doing a crazy variable check loop to see if a buffer is ready to be written to). No speed hit, but on a 15-second copy, without this, it also uses 15 seconds of CPU time. With this, just over 1s CPU time. So the CPU no longer runs host on copies.
