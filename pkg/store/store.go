package store

import "errors"

// ErrNotFound is returned when a requested resource does not exist.
var ErrNotFound = errors.New("not found")

// ErrEmailTaken is returned when a user attempts to register with an email already in use.
var ErrEmailTaken = errors.New("email already taken")

// ErrLastAdmin is returned when attempting to demote or delete the last admin user.
var ErrLastAdmin = errors.New("cannot remove the last admin")
