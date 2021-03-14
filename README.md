sflag is a small/simple/structure enhancement of stdlib `flag`

# Features
* support subcommand
* define/parse flags with structure
* catch non-flag values into structure field tagged with `name:"#nonflag"`

# Usage
structure tags:
* name: flag name without dash prefix, separate multiple names by comma
* usage: flag usage/description
* default: flag default value

catch non-flag values with `name:"#nonflag"`
* allows multiple non-flag field, catch by order of appearance if have more than one.
* support both `string/[]string` to catch one or more values, but can have only one field with type `[]string`
* non-flag value will not allowed if there are no non-flag fields.

# License
MIT.