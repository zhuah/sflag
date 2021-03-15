sflag is a small/simple/structure enhancement of stdlib `flag`

# Features
* support subcommand with global options
* define/parse flags with structure
* catch non-flag values into structure field tagged with `name:"#"` or `name:"#CUSTOM_DISPLAY_NAME"`

# Usage
structure tags:
* name: flag name without dash prefix, separate multiple names by comma
* usage: flag usage/description
* env: get value from environment variable
* default: flag default value

catch non-flag values with `name:"#"` or `name:"#CUSTOM_DISPLAY_NAME"`
* allows multiple non-flag field, catch by order of appearance if have more than one.
* support both `string/[]string` to catch one or more values, but can have only one field with type `[]string`
* non-flag value will not allowed if there are no non-flag fields.

# License
MIT.