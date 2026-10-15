// Jest stand-in for SVG imports. The react-scripts transform builds the
// ReactComponent by hand with the React 18 element tag, which React 19
// refuses to render, so icons come from here under test.
import React from 'react';

export default 'svg';
export const ReactComponent = (props) => React.createElement('svg', props);
