// fibre-assign — an independent recompute of Celestia Fibre's shard assignment
// (which validator must serve which blob rows). Zero dependencies; the
// differential test that proves it bit-identical to celestia-app lives in the
// nested ./reftest module, which is not part of this package's dependency set.
module github.com/plsgiveup/fibre/fibre-assign

go 1.23
